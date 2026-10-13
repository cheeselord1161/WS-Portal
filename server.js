require('dotenv').config();
const express = require('express');
const cors = require('cors');
const crypto = require('crypto');
const fs = require('fs');
const path = require('path');
const admin = require('firebase-admin');
const hub = require('./lib/hub');

const fetch = globalThis.fetch;
const SITE_URL = (process.env.FRONTEND_URL || 'https://forkd-ai.com').replace(/\/+$/, '');

if (!process.env.FIREBASE_SERVICE_ACCOUNT) {
  console.error('FIREBASE_SERVICE_ACCOUNT environment variable is not set');
  process.exit(1);
}
const serviceAccount = JSON.parse(process.env.FIREBASE_SERVICE_ACCOUNT);
admin.initializeApp({ credential: admin.credential.cert(serviceAccount) });
const db = admin.firestore();

const app = express();
const PORT = process.env.PORT || 3000;

app.use(cors({
  origin: process.env.FRONTEND_URL || 'https://forkd-ai.com',
  credentials: true
}));
app.use(express.json({
  verify: (req, res, buf) => { req.rawBody = buf; }
}));
app.use(express.static('public'));

app.get('/health', (req, res) => res.status(200).send('OK'));

// ── PLAN CONFIG ──
const DAILY_LIMITS = { free: 25, pro: 50 };

function getTodayKey(date = new Date()) {
  return date.toISOString().split('T')[0];
}

function getPlanConfig(plan) {
  return {
    free: { maxTokens: 4000, cooldownMs: 0, dailyLimit: DAILY_LIMITS.free },
    pro:  { maxTokens: 6000, cooldownMs: 0, dailyLimit: DAILY_LIMITS.pro }
  }[plan] || { maxTokens: 4000, cooldownMs: 0, dailyLimit: 0 };
}

// ── SHARED: verify token + check limits ──
async function verifyAndCheckLimits(req, res) {
  const authHeader = req.headers.authorization || '';
  const idToken = authHeader.startsWith('Bearer ') ? authHeader.slice(7) : null;
  if (!idToken) { res.status(401).json({ error: 'Unauthorized: no token provided' }); return null; }

  let decodedToken;
  try {
    decodedToken = await admin.auth().verifyIdToken(idToken);
  } catch(err) {
    console.error('Token verification failed:', err.message);
    res.status(401).json({ error: 'Unauthorized: invalid token' }); return null;
  }

  const uid = decodedToken.uid;
  const userRef = db.collection('users').doc(uid);
  const today = getTodayKey();
  const usageKey = `usage_${today}`;
  const lastGenKey = `lastGen_${today}`;

  let userData = {};
  try {
    const snap = await userRef.get();
    userData = snap.exists ? snap.data() : {};
  } catch(err) {
    console.error('Firestore read error:', err.message);
    // Allow generation if Firestore temporarily unavailable
  }

  const userPlan = userData.status || 'free';
  const planConfig = getPlanConfig(userPlan);
  const currentUsage = userData[usageKey] || 0;
  const lastGenTime = userData[lastGenKey] || 0;
  const now = Date.now();

  // Cooldown check (Pro only)
  if (userPlan === 'pro' && planConfig.cooldownMs > 0) {
    const timeSinceLast = now - lastGenTime;
    if (timeSinceLast < planConfig.cooldownMs) {
      const waitSec = Math.ceil((planConfig.cooldownMs - timeSinceLast) / 1000);
      res.status(429).json({ error: 'cooldown', message: `Please wait ${waitSec}s before generating again.`, waitSeconds: waitSec });
      return null;
    }
  }

  // Daily limit check
  if (Number.isFinite(planConfig.dailyLimit) && currentUsage >= planConfig.dailyLimit) {
    res.status(429).json({ error: 'limit_reached', message: 'Daily limit reached. Resets at midnight UTC.', currentUsage, dailyLimit: planConfig.dailyLimit, plan: userPlan });
    return null;
  }

  return { uid, userRef, userPlan, planConfig, currentUsage, usageKey, lastGenKey, now };
}

// ── SHARED: increment usage ──
async function incrementUsage(userRef, usageKey, lastGenKey, currentUsage, now) {
  try {
    await userRef.set({
      [usageKey]: currentUsage + 1,
      [lastGenKey]: now,
      totalGenerations: admin.firestore.FieldValue.increment(1),
      updatedAt: admin.firestore.FieldValue.serverTimestamp()
    }, { merge: true });
  } catch(err) {
    console.error('Failed to update usage:', err.message);
  }
}

// ── SHARED: build Mistral prompt ──
function buildPrompt(commits, sections, templateInstruction, langInstruction) {
  const sectionList = sections || 'Added, Changed, Fixed, Removed';
  return `You are a professional technical writer specializing in software changelogs.
Convert the following git commits into a polished, user-friendly changelog entry.

Rules:
- Rewrite each commit into a clear, benefit-focused sentence
- Group under these sections ONLY (skip sections with no entries): ${sectionList}
- Write for end users, not developers
- Each entry explains the IMPACT, not just what changed
- Be concise but descriptive — one sentence per item
- Skip chore/dependency updates unless critical${templateInstruction || ''}${langInstruction || ''}

Commits:
${commits}

Output format (use ## for section headings, - for entries):
## [Section Name]
- [user-friendly description]`;
}

// ── GENERATE (standard JSON response) ──
app.post('/generate', async (req, res) => {
  const { commits, sections, templateInstruction, langInstruction } = req.body;
  if (!commits) return res.status(400).json({ error: 'No commits provided' });

  const ctx = await verifyAndCheckLimits(req, res);
  if (!ctx) return;

  if (!process.env.MISTRAL_API_KEY) {
    return res.status(500).json({ error: 'Server configuration error: MISTRAL_API_KEY not set' });
  }

  try {
    const response = await fetch('https://api.mistral.ai/v1/chat/completions', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json', 'Authorization': `Bearer ${process.env.MISTRAL_API_KEY}` },
      body: JSON.stringify({
        model: 'mistral-small-latest',
        max_tokens: ctx.planConfig.maxTokens,
        messages: [{ role: 'user', content: buildPrompt(commits, sections, templateInstruction, langInstruction) }]
      })
    });

    if (!response.ok) {
      const errorText = await response.text();
      console.error('Mistral API error', response.status, errorText);
      return res.status(500).json({ error: 'AI service error', status: response.status, detail: errorText });
    }

    const data = await response.json();
    const changelog = data.choices?.[0]?.message?.content;
    if (!changelog) {
      console.error('Unexpected Mistral response', JSON.stringify(data).slice(0, 500));
      return res.status(500).json({ error: 'Failed to generate changelog' });
    }

    await incrementUsage(ctx.userRef, ctx.usageKey, ctx.lastGenKey, ctx.currentUsage, ctx.now);

    res.json({
      changelog,
      usage: { current: ctx.currentUsage + 1, limit: ctx.planConfig.dailyLimit, plan: ctx.userPlan }
    });
  } catch(err) {
    console.error('Generate route error:', err.message);
    res.status(500).json({ error: 'Server error', detail: err.message });
  }
});

// ── GENERATE WITH SSE PROGRESS ──
// Client connects here to receive step-by-step progress events, then the final changelog.
// Usage: POST /generate-stream  (same body as /generate)
// The client sends the body as a query param payload for SSE GET, or we use a token approach.
// Since SSE requires GET, we accept the payload via a one-time token registered via POST first.

// Step 1: client POSTs to /generate-stream/init to register payload, gets a streamToken
// Step 2: client opens EventSource to /generate-stream?token=<streamToken>
const streamTokens = new Map(); // token -> { body, ts }

app.post('/generate-stream/init', async (req, res) => {
  // Just validate auth + limits here so the SSE stream can start clean
  const { commits } = req.body;
  if (!commits) return res.status(400).json({ error: 'No commits provided' });

  const ctx = await verifyAndCheckLimits(req, res);
  if (!ctx) return;

  const token = crypto.randomBytes(16).toString('hex');
  streamTokens.set(token, { body: req.body, ctx, ts: Date.now() });

  // Clean up stale tokens after 2 minutes
  setTimeout(() => streamTokens.delete(token), 120000);

  res.json({ token });
});

app.get('/generate-stream', async (req, res) => {
  const token = req.query.token;
  const entry = streamTokens.get(token);
  if (!entry) return res.status(400).json({ error: 'Invalid or expired stream token' });
  streamTokens.delete(token);

  const { body, ctx } = entry;
  const { commits, sections, templateInstruction, langInstruction } = body;

  // Set SSE headers
  res.setHeader('Content-Type', 'text/event-stream');
  res.setHeader('Cache-Control', 'no-cache');
  res.setHeader('Connection', 'keep-alive');
  res.flushHeaders();

  function sendEvent(event, data) {
    res.write(`event: ${event}\ndata: ${JSON.stringify(data)}\n\n`);
  }

  try {
    sendEvent('progress', { step: 1, label: 'Sending to AI…' });

    if (!process.env.MISTRAL_API_KEY) {
      sendEvent('error', { message: 'Server configuration error' });
      return res.end();
    }

    sendEvent('progress', { step: 2, label: 'Generating changelog…' });

    const response = await fetch('https://api.mistral.ai/v1/chat/completions', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json', 'Authorization': `Bearer ${process.env.MISTRAL_API_KEY}` },
      body: JSON.stringify({
        model: 'mistral-small-latest',
        max_tokens: ctx.planConfig.maxTokens,
        messages: [{ role: 'user', content: buildPrompt(commits, sections, templateInstruction, langInstruction) }]
      })
    });

    if (!response.ok) {
      const errorText = await response.text();
      sendEvent('error', { message: `AI service error ${response.status}`, detail: errorText });
      return res.end();
    }

    const data = await response.json();
    const changelog = data.choices?.[0]?.message?.content;
    if (!changelog) {
      sendEvent('error', { message: 'No changelog generated' });
      return res.end();
    }

    sendEvent('progress', { step: 3, label: 'Finalizing…' });
    await incrementUsage(ctx.userRef, ctx.usageKey, ctx.lastGenKey, ctx.currentUsage, ctx.now);

    sendEvent('done', {
      changelog,
      usage: { current: ctx.currentUsage + 1, limit: ctx.planConfig.dailyLimit, plan: ctx.userPlan }
    });

    res.end();
  } catch(err) {
    console.error('SSE stream error:', err.message);
    sendEvent('error', { message: err.message });
    res.end();
  }
});

// ── LEMON SQUEEZY WEBHOOK ──
app.post('/lemon-squeezy-webhook', async (req, res) => {
  if (!process.env.LEMON_SQUEEZY_WEBHOOK_SECRET) {
    console.error('LEMON_SQUEEZY_WEBHOOK_SECRET not set');
    return res.status(500).send('Server configuration error');
  }

  const hmac = crypto.createHmac('sha256', process.env.LEMON_SQUEEZY_WEBHOOK_SECRET);
  const digest = Buffer.from(hmac.update(req.rawBody).digest('hex'), 'utf8');
  const signature = Buffer.from(req.get('X-Signature') || '', 'utf8');

  if (digest.length !== signature.length || !crypto.timingSafeEqual(digest, signature)) {
    return res.status(401).send('Invalid signature');
  }

  const eventName = req.body['meta']['event_name'];
  const customData = req.body['meta']['custom_data'] || {};
  const userId = customData['user_id'] || null;

  if (!userId) return res.status(200).send('Webhook received but no user_id found');

  try {
    const userRef = db.collection('users').doc(userId);
    switch (eventName) {
      case 'order_created':
      case 'subscription_created':
      case 'subscription_updated':
      case 'subscription_payment_success': {
        const subscriptionId = req.body['data']?.['id'];
        const endsAt = req.body['data']?.['attributes']?.['ends_at'];
        await userRef.set({ status: 'pro', subscriptionId, subscriptionEndsAt: endsAt, subscriptionCancelled: false, updatedAt: admin.firestore.FieldValue.serverTimestamp() }, { merge: true });
        console.log(`User ${userId} promoted to PRO (sub: ${subscriptionId})`);
        break;
      }
      case 'subscription_cancelled': {
        const cancelledEndsAt = req.body['data']?.['attributes']?.['ends_at'];
        await userRef.set({ status: 'pro', subscriptionEndsAt: cancelledEndsAt, subscriptionCancelled: true, updatedAt: admin.firestore.FieldValue.serverTimestamp() }, { merge: true });
        console.log(`User ${userId} cancelled (ends ${cancelledEndsAt})`);
        break;
      }
      case 'subscription_expired':
      case 'subscription_payment_failed':
      case 'order_refunded':
        await userRef.set({ status: 'free', subscriptionId: null, subscriptionCancelled: false, subscriptionEndsAt: null, updatedAt: admin.firestore.FieldValue.serverTimestamp() }, { merge: true });
        console.log(`User ${userId} reverted to FREE`);
        break;
    }
  } catch(err) {
    console.error('Firestore webhook error:', err);
  }

  res.status(200).send('Webhook Processed');
});

// ── CANCEL SUBSCRIPTION ──
app.post('/cancel-subscription', async (req, res) => {
  const authHeader = req.headers.authorization || '';
  const idToken = authHeader.startsWith('Bearer ') ? authHeader.slice(7) : null;
  if (!idToken) return res.status(401).json({ error: 'Unauthorized: no token provided' });

  let decodedToken;
  try {
    decodedToken = await admin.auth().verifyIdToken(idToken);
  } catch(err) {
    return res.status(401).json({ error: 'Unauthorized: invalid token' });
  }

  const uid = decodedToken.uid;
  const userRef = db.collection('users').doc(uid);

  try {
    const snap = await userRef.get();
    const userData = snap.data() || {};
    const subscriptionId = userData.subscriptionId;
    if (!subscriptionId) return res.status(400).json({ error: 'No active subscription found' });

    if (!process.env.LEMON_SQUEEZY_API_KEY) {
      return res.status(500).json({ error: 'Server configuration error: LEMON_SQUEEZY_API_KEY not set' });
    }

    const cancelResponse = await fetch(`https://api.lemonsqueezy.com/v1/subscriptions/${subscriptionId}`, {
      method: 'PATCH',
      headers: { 'Content-Type': 'application/json', 'Authorization': `Bearer ${process.env.LEMON_SQUEEZY_API_KEY}` },
      body: JSON.stringify({ data: { type: 'subscriptions', id: subscriptionId, attributes: { cancelled: true } } })
    });

    if (!cancelResponse.ok) {
      const errorText = await cancelResponse.text();
      console.error('Lemon Squeezy cancel error', cancelResponse.status, errorText);
      return res.status(500).json({ error: 'Failed to cancel with provider', detail: errorText });
    }

    const cancelData = await cancelResponse.json();
    const endsAt = cancelData.data?.attributes?.['ends_at'];

    await userRef.set({ subscriptionCancelled: true, subscriptionEndsAt: endsAt, updatedAt: admin.firestore.FieldValue.serverTimestamp() }, { merge: true });
    console.log(`User ${uid} cancelled subscription (ends ${endsAt})`);

    res.status(200).json({ success: true, message: 'Subscription cancelled', endsAt, status: 'pro' });
  } catch(err) {
    console.error('Cancel subscription error:', err.message);
    res.status(500).json({ error: 'Server error', detail: err.message });
  }
});

// ── STORE GITHUB ID ──
app.post('/store-github-id', async (req, res) => {
  const authHeader = req.headers.authorization || '';
  const idToken = authHeader.startsWith('Bearer ') ? authHeader.slice(7) : null;
  if (!idToken) return res.status(401).json({ error: 'Unauthorized' });

  const { githubId } = req.body;
  if (!githubId) return res.status(400).json({ error: 'No GitHub ID provided' });

  try {
    const decodedToken = await admin.auth().verifyIdToken(idToken);
    const uid = decodedToken.uid;
    const userRef = db.collection('users').doc(uid);

    await userRef.set({
      githubId: githubId,
      updatedAt: admin.firestore.FieldValue.serverTimestamp()
    }, { merge: true });

    console.log(`Stored GitHub ID "${githubId}" for user ${uid}`);
    res.json({ success: true, message: 'GitHub ID stored' });
  } catch(err) {
    console.error('Store GitHub ID error:', err.message);
    res.status(500).json({ error: 'Server error', detail: err.message });
  }
});

// ── SHARED CHANGELOGS ──
app.post('/shared-changelogs', async (req, res) => {
  const authHeader = req.headers.authorization || '';
  const idToken = authHeader.startsWith('Bearer ') ? authHeader.slice(7) : null;
  if (!idToken) return res.status(401).json({ error: 'Unauthorized' });

  const { title, changelog, version } = req.body;
  if (!changelog) return res.status(400).json({ error: 'No changelog content provided' });

  try {
    const decodedToken = await admin.auth().verifyIdToken(idToken);
    const uid = decodedToken.uid;
    
    // Save user's display name for hub profile
    const userRef = db.collection('users').doc(uid);
    if (decodedToken.name || decodedToken.email) {
      await userRef.set({
        displayName: decodedToken.name || null,
        email: decodedToken.email || null,
        photoURL: decodedToken.picture || null
      }, { merge: true });
    }
    
    const shareId = crypto.randomBytes(8).toString('hex');
    const shareRef = db.collection('shared_changelogs').doc(shareId);
    
    // Get author name from decoded token or user doc
    let authorName = decodedToken.name || null;
    if (!authorName) {
      try {
        const userSnap = await db.collection('users').doc(uid).get();
        if (userSnap.exists) authorName = userSnap.data().displayName || null;
      } catch(e) {}
    }
    if (!authorName) authorName = decodedToken.email ? decodedToken.email.split('@')[0] : 'Anonymous';

    await shareRef.set({
      uid,
      authorName,
      title: title || 'Untitled Changelog',
      changelog,
      version: version || '',
      createdAt: admin.firestore.FieldValue.serverTimestamp(),
      views: 0,
      starCount: 0
    });

    console.log(`User ${uid} shared changelog as ${shareId}`);
    res.json({ shareId, url: `/shared/${shareId}` });
  } catch(err) {
    console.error('Share changelog error:', err.message);
    res.status(500).json({ error: 'Server error', detail: err.message });
  }
});

// View a shared changelog (no auth required)
app.get('/shared-changelogs/:id', async (req, res) => {
  const { id } = req.params;
  try {
    const snap = await db.collection('shared_changelogs').doc(id).get();
    if (!snap.exists) {
      return res.status(404).json({ error: 'Changelog not found' });
    }
    
    const data = snap.data();

    // Drafts are never publicly readable through the legacy share endpoint.
    if (data.status === 'draft') {
      return res.status(404).json({ error: 'Changelog not found' });
    }

    // Increment view count (fire and forget)
    snap.ref.update({ views: admin.firestore.FieldValue.increment(1) }).catch(() => {});
    
    res.json({
      id,
      uid: data.uid,
      authorName: data.authorName || 'Anonymous',
      title: data.title,
      changelog: data.changelog,
      version: data.version,
      createdAt: data.createdAt?.toDate()?.toISOString() || null
    });
  } catch(err) {
    console.error('View shared changelog error:', err.message);
    res.status(500).json({ error: 'Server error' });
  }
});

// List user's shared changelogs
app.get('/user-shared-changelogs', async (req, res) => {
  const authHeader = req.headers.authorization || '';
  const idToken = authHeader.startsWith('Bearer ') ? authHeader.slice(7) : null;
  if (!idToken) return res.status(401).json({ error: 'Unauthorized' });

  try {
    const decodedToken = await admin.auth().verifyIdToken(idToken);
    const uid = decodedToken.uid;

    const snapshot = await db.collection('shared_changelogs')
      .where('uid', '==', uid)
      .get();

    const items = [];
    snapshot.forEach(doc => {
      const data = doc.data();
      items.push({
        id: doc.id,
        title: data.title,
        version: data.version,
        views: data.views || 0,
        createdAt: data.createdAt?.toDate()?.toISOString() || null
      });
    });

    // Sort by createdAt descending (in-memory to avoid composite index requirement)
    items.sort((a, b) => {
      if (!a.createdAt) return 1;
      if (!b.createdAt) return -1;
      return new Date(b.createdAt) - new Date(a.createdAt);
    });

    // Limit to 50 after sorting
    res.json({ items: items.slice(0, 50) });
  } catch(err) {
    console.error('User shared changelogs error:', err.message);
    res.status(500).json({ error: 'Server error' });
  }
});

// Delete a shared changelog
app.delete('/shared-changelogs/:id', async (req, res) => {
  const authHeader = req.headers.authorization || '';
  const idToken = authHeader.startsWith('Bearer ') ? authHeader.slice(7) : null;
  if (!idToken) return res.status(401).json({ error: 'Unauthorized' });

  try {
    const decodedToken = await admin.auth().verifyIdToken(idToken);
    const uid = decodedToken.uid;
    const { id } = req.params;

    const snap = await db.collection('shared_changelogs').doc(id).get();
    if (!snap.exists) return res.status(404).json({ error: 'Not found' });
    if (snap.data().uid !== uid) return res.status(403).json({ error: 'Forbidden' });

    await snap.ref.delete();
    res.json({ success: true });
  } catch(err) {
    console.error('Delete shared changelog error:', err.message);
    res.status(500).json({ error: 'Server error' });
  }
});

// ── PUSH TO GITHUB RELEASES ──
app.post('/github-release', async (req, res) => {
  const authHeader = req.headers.authorization || '';
  const idToken = authHeader.startsWith('Bearer ') ? authHeader.slice(7) : null;
  if (!idToken) return res.status(401).json({ error: 'Unauthorized' });

  const { owner, repo, tag_name, name, body } = req.body;
  if (!owner || !repo || !tag_name || !body) {
    return res.status(400).json({ error: 'Missing required fields: owner, repo, tag_name, body' });
  }

  try {
    const decodedToken = await admin.auth().verifyIdToken(idToken);
    const uid = decodedToken.uid;

    if (!process.env.GITHUB_PAT) {
      return res.status(400).json({
        error: 'GitHub PAT not configured',
        guide: 'Set GITHUB_PAT environment variable with a personal access token that has repo permissions.',
        manualBody: body
      });
    }

    const releaseResponse = await fetch(`https://api.github.com/repos/${owner}/${repo}/releases`, {
      method: 'POST',
      headers: {
        'Accept': 'application/vnd.github.v3+json',
        'Authorization': `Bearer ${process.env.GITHUB_PAT}`,
        'User-Agent': 'Forkd-AI',
        'Content-Type': 'application/json'
      },
      body: JSON.stringify({
        tag_name,
        name: name || tag_name,
        body,
        draft: false,
        prerelease: false
      })
    });

    const releaseData = await releaseResponse.json();

    if (!releaseResponse.ok) {
      console.error('GitHub release error', releaseResponse.status, releaseData);
      return res.status(400).json({
        error: 'Failed to create release',
        message: releaseData.message || 'Unknown GitHub error',
        manualBody: body
      });
    }

    console.log(`User ${uid} created GitHub release ${tag_name} on ${owner}/${repo}`);
    res.json({
      success: true,
      url: releaseData.html_url,
      id: releaseData.id,
      tag_name: releaseData.tag_name
    });
  } catch(err) {
    console.error('GitHub release error:', err.message);
    res.status(500).json({ error: 'Server error', detail: err.message });
  }
});

// ── GITHUB REPOS ──
app.get('/github-repos', async (req, res) => {
  const authHeader = req.headers.authorization || '';
  const idToken = authHeader.startsWith('Bearer ') ? authHeader.slice(7) : null;
  if (!idToken) return res.status(401).json({ error: 'Unauthorized' });

  try {
    const decodedToken = await admin.auth().verifyIdToken(idToken);
    const uid = decodedToken.uid;
    const userRef = db.collection('users').doc(uid);
    const userSnap = await userRef.get();
    const userData = userSnap.data() || {};

      // Get GitHub ID from Firestore
    const githubId = userData.githubId;
    if (!githubId) {
      console.warn(`User ${uid} has no GitHub ID stored`);
      return res.status(400).json({ 
        error: 'GitHub not linked',
        message: 'Please link your GitHub account in the Dashboard → Account section first.'
      });
    }

    console.log(`Fetching GitHub username for ID ${githubId}...`);

    // Fetch GitHub user info to get username
    const userInfoResponse = await fetch(`https://api.github.com/user/${githubId}`, {
      headers: {
        'Accept': 'application/vnd.github.v3+json',
        'User-Agent': 'Forkd-AI'
      }
    });

    if (!userInfoResponse.ok) {
      console.error(`GitHub user info error: ${userInfoResponse.status}`);
      return res.status(400).json({ 
        error: 'GitHub user lookup failed',
        message: 'Could not find GitHub user. The account link may be invalid.'
      });
    }

    const userInfo = await userInfoResponse.json();
    const githubUsername = userInfo.login;
    console.log(`Resolved GitHub ID ${githubId} to username: ${githubUsername}`);

    // Now fetch repos
    console.log(`Fetching repos for user ${uid} (GitHub: ${githubUsername})...`);
    const reposResponse = await fetch(`https://api.github.com/users/${githubUsername}/repos?per_page=100&sort=updated&direction=desc`, {
      headers: {
        'Accept': 'application/vnd.github.v3+json',
        'User-Agent': 'Forkd-AI'
      }
    });

    if (!reposResponse.ok) {
      console.error(`GitHub repos error: ${reposResponse.status}`);
      return res.status(400).json({ 
        error: 'Failed to fetch repos',
        message: 'Could not fetch repos from GitHub. Please try again.'
      });
    }

    const repos = await reposResponse.json();
    const formattedRepos = repos.map(repo => ({
      name: repo.name,
      owner: repo.owner.login,
      description: repo.description || '',
      url: repo.html_url,
      stars: repo.stargazers_count
    }));

    console.log(`Returned ${formattedRepos.length} repos for user ${uid}`);
    res.json({ repos: formattedRepos });
  } catch(err) {
    console.error('GitHub repos error:', err.message);
    res.status(500).json({ error: 'Server error', detail: err.message });
  }
});

// ── SHARED BRANDING ──
// Returns branding config for the owner of a shared changelog
app.get('/shared-branding/:shareId', async (req, res) => {
  try {
    const snap = await db.collection('shared_changelogs').doc(req.params.shareId).get();
    if (!snap.exists) return res.json({});
    const ownerUid = snap.data().uid;
    if (!ownerUid) return res.json({});

    const brandSnap = await db.collection('branding').doc(ownerUid).get();
    if (!brandSnap.exists) return res.json({});

    res.json(brandSnap.data());
  } catch(err) {
    console.error('Branding fetch error:', err.message);
    res.json({});
  }
});

// ── SAVE PROFILE (bio + displayName) ──
app.post('/user/profile', async (req, res) => {
  const authHeader = req.headers.authorization || '';
  const idToken = authHeader.startsWith('Bearer ') ? authHeader.slice(7) : null;
  if (!idToken) return res.status(401).json({ error: 'Unauthorized' });

  try {
    const decoded = await admin.auth().verifyIdToken(idToken);
    const uid = decoded.uid;
    const { bio, displayName } = req.body;
    await db.collection('users').doc(uid).set({
      bio: bio || '',
      displayName: displayName || '',
      updatedAt: admin.firestore.FieldValue.serverTimestamp()
    }, { merge: true });
    console.log(`Profile saved for user ${uid}`);
    res.json({ success: true });
  } catch(err) {
    console.error('Save profile error:', err.message);
    res.status(500).json({ error: 'Server error' });
  }
});

// ── SAVE BRANDING (Pro only) ──
app.post('/user/branding', async (req, res) => {
  const authHeader = req.headers.authorization || '';
  const idToken = authHeader.startsWith('Bearer ') ? authHeader.slice(7) : null;
  if (!idToken) return res.status(401).json({ error: 'Unauthorized' });

  try {
    const decoded = await admin.auth().verifyIdToken(idToken);
    const uid = decoded.uid;
    const userSnap = await db.collection('users').doc(uid).get();
    const userData = userSnap.data() || {};
    if (userData.status !== 'pro') return res.status(403).json({ error: 'Pro plan required' });

    const { logoUrl, accentColor, bgColor, brandName, hideBranding } = req.body;
    const brandRef = db.collection('branding').doc(uid);
    await brandRef.set({
      logoUrl: logoUrl || '',
      accentColor: accentColor || '#ffffff',
      bgColor: bgColor || '#030305',
      brandName: brandName || '',
      hideBranding: hideBranding || false,
      updatedAt: admin.firestore.FieldValue.serverTimestamp()
    }, { merge: true });

    res.json({ success: true });
  } catch(err) {
    console.error('Branding save error:', err.message);
    res.status(500).json({ error: 'Server error' });
  }
});

// ── GET BRANDING ──
app.get('/user/branding', async (req, res) => {
  const authHeader = req.headers.authorization || '';
  const idToken = authHeader.startsWith('Bearer ') ? authHeader.slice(7) : null;
  if (!idToken) return res.status(401).json({ error: 'Unauthorized' });

  try {
    const decoded = await admin.auth().verifyIdToken(idToken);
    const uid = decoded.uid;
    const brandSnap = await db.collection('branding').doc(uid).get();
    if (!brandSnap.exists) return res.json({});
    res.json(brandSnap.data());
  } catch(err) {
    console.error('Branding get error:', err.message);
    res.status(500).json({ error: 'Server error' });
  }
});

// ── API KEY MANAGEMENT (Pro) ──
// API keys stored in a separate api_keys collection with key as doc ID (no index needed)
app.post('/user/api-key/generate', async (req, res) => {
  const authHeader = req.headers.authorization || '';
  const idToken = authHeader.startsWith('Bearer ') ? authHeader.slice(7) : null;
  if (!idToken) return res.status(401).json({ error: 'Unauthorized' });

  try {
    const decoded = await admin.auth().verifyIdToken(idToken);
    const uid = decoded.uid;
    const userSnap = await db.collection('users').doc(uid).get();
    const userData = userSnap.data() || {};
    if (userData.status !== 'pro') return res.status(403).json({ error: 'Pro plan required' });

    // Revoke old key if exists
    const oldKey = userData.apiKey;
    if (oldKey) {
      try { await db.collection('api_keys').doc(oldKey).delete(); } catch(e) {}
    }

    const apiKey = 'forkd_' + crypto.randomBytes(24).toString('hex');
    
    // Store in api_keys collection (doc ID = key, so no index needed)
    await db.collection('api_keys').doc(apiKey).set({
      uid,
      plan: 'pro',
      createdAt: admin.firestore.FieldValue.serverTimestamp()
    });

    // Reference on user doc
    await db.collection('users').doc(uid).set({ apiKey }, { merge: true });

    console.log(`API key generated for user ${uid}`);
    res.json({ apiKey });
  } catch(err) {
    console.error('API key generate error:', err.message);
    res.status(500).json({ error: 'Server error' });
  }
});

app.delete('/user/api-key', async (req, res) => {
  const authHeader = req.headers.authorization || '';
  const idToken = authHeader.startsWith('Bearer ') ? authHeader.slice(7) : null;
  if (!idToken) return res.status(401).json({ error: 'Unauthorized' });

  try {
    const decoded = await admin.auth().verifyIdToken(idToken);
    const uid = decoded.uid;
    const userSnap = await db.collection('users').doc(uid).get();
    const oldKey = (userSnap.data() || {}).apiKey;
    if (oldKey) {
      try { await db.collection('api_keys').doc(oldKey).delete(); } catch(e) {}
    }
    await db.collection('users').doc(uid).set({ apiKey: null }, { merge: true });
    res.json({ success: true });
  } catch(err) {
    console.error('API key revoke error:', err.message);
    res.status(500).json({ error: 'Server error' });
  }
});

// ── Rate limiter for API endpoint ──
const apiRateLimits = new Map(); // apiKey -> { count, resetAt }
function checkRateLimit(apiKey) {
  const now = Date.now();
  const entry = apiRateLimits.get(apiKey);
  if (!entry || now > entry.resetAt) {
    apiRateLimits.set(apiKey, { count: 1, resetAt: now + 60000 });
    return true;
  }
  if (entry.count >= 10) return false; // max 10 req/min
  entry.count++;
  return true;
}

// ── Generic keyed rate limiter (public submission + publishing endpoints) ──
const actionRateLimits = new Map(); // "bucket:key" -> { count, resetAt }
function rateLimit(bucket, key, max, windowMs) {
  const now = Date.now();
  const id = `${bucket}:${key || 'anonymous'}`;
  const entry = actionRateLimits.get(id);
  if (!entry || now > entry.resetAt) {
    actionRateLimits.set(id, { count: 1, resetAt: now + windowMs });
    return { allowed: true, remaining: max - 1, resetAt: now + windowMs };
  }
  if (entry.count >= max) return { allowed: false, remaining: 0, resetAt: entry.resetAt };
  entry.count++;
  return { allowed: true, remaining: max - entry.count, resetAt: entry.resetAt };
}

function clientKey(req) {
  const forwarded = req.headers['x-forwarded-for'];
  if (typeof forwarded === 'string' && forwarded.length) return forwarded.split(',')[0].trim();
  return req.ip || req.socket?.remoteAddress || 'unknown';
}

// Clean up old rate limit entries every 5 minutes
setInterval(() => {
  const now = Date.now();
  for (const [key, entry] of apiRateLimits) {
    if (now > entry.resetAt) apiRateLimits.delete(key);
  }
  for (const [key, entry] of actionRateLimits) {
    if (now > entry.resetAt) actionRateLimits.delete(key);
  }
}, 300000);

// ── API GENERATE ENDPOINT (API key auth) ──
app.post('/api/generate', async (req, res) => {
  const authHeader = req.headers.authorization || '';
  const apiKey = authHeader.startsWith('Bearer ') ? authHeader.slice(7) : null;
  if (!apiKey) return res.status(401).json({ error: 'Missing API key. Include as: Authorization: Bearer <your_key>' });

  // Rate limit check
  if (!checkRateLimit(apiKey)) {
    return res.status(429).json({ error: 'Rate limit exceeded. Max 10 requests per minute.' });
  }

  const { commits, template, language, version, sections } = req.body;
  if (!commits) return res.status(400).json({ error: 'No commits provided' });

  try {
    // Look up API key directly in api_keys collection (doc ID = key, no index needed)
    const apiKeyDoc = await db.collection('api_keys').doc(apiKey).get();
    if (!apiKeyDoc.exists) return res.status(401).json({ error: 'Invalid API key' });

    const keyData = apiKeyDoc.data();
    const uid = keyData.uid;

    if (keyData.plan !== 'pro') return res.status(403).json({ error: 'API access requires Pro plan' });

    // Get user data for usage tracking
    const userSnap = await db.collection('users').doc(uid).get();
    const userData = userSnap.data() || {};

    const planConfig = getPlanConfig('pro');
    const today = getTodayKey();
    const usageKey = `usage_${today}`;
    const currentUsage = userData[usageKey] || 0;

    if (currentUsage >= planConfig.dailyLimit) {
      return res.status(429).json({ error: 'Daily limit reached', used: currentUsage, limit: planConfig.dailyLimit });
    }

    // Build prompt
    const TEMPLATE_MAP = {
      compact: 'Be very concise — one short sentence per item.',
      detailed: 'Be thorough. Include impact, affected areas, and migration notes.',
      keepachangelog: 'Follow the Keep a Changelog format. Use only: Added, Changed, Deprecated, Removed, Fixed, Security.',
      'release-notes': 'Write in a friendly, user-facing release notes tone. Lead with the most exciting change.',
      default: ''
    };
    const templateInstruction = TEMPLATE_MAP[template] || '';
    const langInstruction = language ? `\nWrite the changelog in ${language}.` : '';
    const sectionList = sections || 'Added, Changed, Fixed, Removed';

    const prompt = buildPrompt(commits, sectionList, templateInstruction, langInstruction);

    if (!process.env.MISTRAL_API_KEY) {
      return res.status(500).json({ error: 'Server configuration error' });
    }

    const response = await fetch('https://api.mistral.ai/v1/chat/completions', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json', 'Authorization': `Bearer ${process.env.MISTRAL_API_KEY}` },
      body: JSON.stringify({
        model: 'mistral-small-latest',
        max_tokens: planConfig.maxTokens,
        messages: [{ role: 'user', content: prompt }]
      })
    });

    if (!response.ok) {
      const errorText = await response.text();
      return res.status(500).json({ error: 'AI service error', detail: errorText });
    }

    const data = await response.json();
    const changelog = data.choices?.[0]?.message?.content;
    if (!changelog) return res.status(500).json({ error: 'Failed to generate changelog' });

    // Track usage
    await db.collection('users').doc(uid).set({
      [usageKey]: currentUsage + 1,
      totalGenerations: admin.firestore.FieldValue.increment(1),
      updatedAt: admin.firestore.FieldValue.serverTimestamp()
    }, { merge: true });

    res.json({ changelog, usage: { current: currentUsage + 1, limit: planConfig.dailyLimit, plan: 'pro' } });
  } catch(err) {
    console.error('API generate error:', err.message);
    res.status(500).json({ error: 'Server error', detail: err.message });
  }
});

// ═══════════════════════════════════════
// ── CHANGELOG HUB ENDPOINTS ──
// ═══════════════════════════════════════

// ── PUBLIC FEED ──
app.get('/api/feed', async (req, res) => {
  const { cursor, limit = 20, search } = req.query;
  try {
    let query = db.collection('shared_changelogs');
    
    // Firestore doesn't support text search natively, so we fetch recent and filter in memory
    const snapshot = await query.get();
    
    let items = [];
    for (const doc of snapshot.docs) {
      const data = doc.data();
      if (data.status === 'draft') continue; // drafts are private
      items.push({
        id: doc.id,
        title: data.title || 'Untitled',
        version: data.version || '',
        views: data.views || 0,
        stars: data.starCount || 0,
        author: data.authorName || 'Anonymous',
        authorId: data.uid,
        createdAt: data.createdAt?.toDate()?.toISOString() || null
      });
    }

    // In-memory sort by newest first
    items.sort((a, b) => {
      if (!a.createdAt) return 1;
      if (!b.createdAt) return -1;
      return new Date(b.createdAt) - new Date(a.createdAt);
    });

    // Text search filter
    if (search) {
      const q = search.toLowerCase();
      items = items.filter(i =>
        i.title.toLowerCase().includes(q) ||
        i.author.toLowerCase().includes(q) ||
        i.version.toLowerCase().includes(q)
      );
    }

    // Cursor-based pagination
    let cursorIndex = 0;
    if (cursor) {
      cursorIndex = items.findIndex(i => i.id === cursor);
      if (cursorIndex === -1) cursorIndex = 0;
    }
    const page = items.slice(cursorIndex, cursorIndex + parseInt(limit));
    const nextCursor = page.length === parseInt(limit) ? page[page.length - 1].id : null;

    res.json({ items: page, nextCursor, total: items.length });
  } catch(err) {
    console.error('Feed error:', err.message);
    res.status(500).json({ error: 'Server error' });
  }
});

// ── FOLLOWING FEED (changelogs from users I follow) ──
app.get('/api/feed/following', async (req, res) => {
  const decoded = await authenticate(req, res);
  if (!decoded) return;

  try {
    const uid = decoded.uid;

    // Read the unified `follows` collection instead of a collection-group scan
    // over every `followers` subcollection: it only holds real follows and never
    // picks up project followers, so the feed is exactly "people you follow".
    const followingSnap = await db.collection(FOLLOWS)
      .where('followerUid', '==', uid)
      .where('targetType', '==', 'user')
      .limit(500)
      .get();

    const followedUids = [];
    followingSnap.forEach(doc => {
      const targetId = doc.data().targetId;
      if (targetId) followedUids.push(targetId);
    });

    if (followedUids.length === 0) {
      return res.json({ items: [], nextCursor: null, followedUsers: 0 });
    }

    let items = [];
    // Firestore allows at most 10 values per `in` query.
    for (let i = 0; i < followedUids.length; i += 10) {
      const snapshot = await db.collection(RELEASES)
        .where('uid', 'in', followedUids.slice(i, i + 10))
        .limit(200)
        .get();
      snapshot.forEach(doc => {
        const data = doc.data();
        if (data.status === 'draft') return; // drafts are private
        items.push({
          id: doc.id,
          slug: data.slug || null,
          title: data.title || 'Untitled',
          version: data.version || '',
          views: data.views || 0,
          stars: data.starCount || 0,
          author: data.authorName || 'Anonymous',
          authorId: data.uid,
          createdAt: data.createdAt?.toDate()?.toISOString() || null
        });
      });
    }

    // Sort by newest
    items.sort((a, b) => {
      if (!a.createdAt) return 1;
      if (!b.createdAt) return -1;
      return new Date(b.createdAt) - new Date(a.createdAt);
    });

    res.json({ items: items.slice(0, 50), nextCursor: null, followedUsers: followedUids.length });
  } catch(err) {
    console.error('Following feed error:', err.message);
    res.status(500).json({ error: 'Server error' });
  }
});

// ── PUBLIC USER PROFILE ──
app.get('/api/users/:uid/profile', async (req, res) => {
  const uid = String(req.params.uid || '');
  if (!uid || uid.length > 128 || uid.includes('/')) return res.status(400).json({ error: 'Invalid user' });
  try {
    const userSnap = await db.collection('users').doc(uid).get();

    // Only published changelogs are public; drafts stay private.
    const changelogsSnap = await db.collection('shared_changelogs').where('uid', '==', uid).get();
    const publicDocs = changelogsSnap.docs.filter(doc => doc.data().status !== 'draft');
    const publicChangelogs = publicDocs.length;

    // A creator can publish without ever saving a profile document, so fall back
    // to the identity carried on their released changelogs instead of 404ing.
    let data = userSnap.exists ? userSnap.data() : null;
    if (!data) {
      if (!publicChangelogs) return res.status(404).json({ error: 'User not found' });
      const latest = publicDocs
        .map(doc => doc.data())
        .sort((a, b) => hub.toMillis(b.publishedAt || b.createdAt) - hub.toMillis(a.publishedAt || a.createdAt))[0] || {};
      data = { displayName: latest.authorName || 'Anonymous' };
    }

    let totalViews = 0;
    let totalStars = 0;
    for (const doc of publicDocs) {
      const release = doc.data();
      totalViews += release.views || 0;
      totalStars += release.starCount || 0;
    }

    res.json({
      uid: req.params.uid,
      displayName: data.displayName || 'Anonymous',
      email: data.email ? data.email.split('@')[0] + '@…' : null,
      photoURL: data.photoURL || null,
      bio: data.bio || '',
      createdAt: data.createdAt?.toDate()?.toISOString() || null,
      stats: {
        publicChangelogs,
        totalViews,
        totalStars
      }
    });
  } catch(err) {
    console.error('User profile error:', err.message);
    res.status(500).json({ error: 'Server error' });
  }
});

// ── USER'S PUBLIC CHANGELOGS ──
app.get('/api/users/:uid/changelogs', async (req, res) => {
  try {
    const snapshot = await db.collection('shared_changelogs')
      .where('uid', '==', req.params.uid)
      .get();

    let items = [];
    for (const doc of snapshot.docs) {
      const data = doc.data();
      if (data.status === 'draft') continue; // drafts are private
      items.push({
        id: doc.id,
        title: data.title || 'Untitled',
        version: data.version || '',
        views: data.views || 0,
        stars: data.starCount || 0,
        createdAt: data.createdAt?.toDate()?.toISOString() || null
      });
    }

    items.sort((a, b) => {
      if (!a.createdAt) return 1;
      if (!b.createdAt) return -1;
      return new Date(b.createdAt) - new Date(a.createdAt);
    });

    res.json({ items });
  } catch(err) {
    console.error('User changelogs error:', err.message);
    res.status(500).json({ error: 'Server error' });
  }
});

// ── STAR / UNSTAR ──
// Stars live in a per-user `stars` subcollection so one account can only ever
// count once, and the denormalized `starCount` is only touched when the star
// document actually changes. Every response carries the authoritative count so
// the client never has to guess (and never drifts after a reload).
const CHANGELOG_ID = /^[a-zA-Z0-9_-]{1,80}$/;

async function changelogStarContext(id) {
  const key = String(id || '');
  if (!CHANGELOG_ID.test(key)) return { error: 'Invalid changelog id', status: 400 };
  const ref = db.collection(RELEASES).doc(key);
  const snap = await ref.get();
  if (!snap.exists) return { error: 'Changelog not found', status: 404 };
  return { ref, data: snap.data() || {} };
}

app.post('/api/changelogs/:id/star', async (req, res) => {
  const decoded = await authenticate(req, res);
  if (!decoded) return;

  try {
    const ctx = await changelogStarContext(req.params.id);
    if (ctx.error) return res.status(ctx.status).json({ error: ctx.error });

    const starRef = ctx.ref.collection('stars').doc(decoded.uid);
    let changed = false;
    const existing = await starRef.get();
    if (!existing.exists) {
      try {
        // create() fails if a concurrent request already wrote the star, which
        // prevents the starCount from being incremented twice for one user.
        await starRef.create({ createdAt: admin.firestore.FieldValue.serverTimestamp() });
        await ctx.ref.update({ starCount: admin.firestore.FieldValue.increment(1) });
        changed = true;
      } catch (err) {
        // gRPC ALREADY_EXISTS (code 6) means another request won the race.
        if (err.code !== 6 && !/already exists/i.test(err.message || '')) throw err;
      }
    }

    const snap = await ctx.ref.get();
    res.json({ starred: true, count: (snap.data() || {}).starCount || 0, changed });
  } catch(err) {
    console.error('Star error:', err.message);
    res.status(500).json({ error: 'Server error' });
  }
});

app.delete('/api/changelogs/:id/star', async (req, res) => {
  const decoded = await authenticate(req, res);
  if (!decoded) return;

  try {
    const ctx = await changelogStarContext(req.params.id);
    if (ctx.error) return res.status(ctx.status).json({ error: ctx.error });

    const starRef = ctx.ref.collection('stars').doc(decoded.uid);
    // Only decrement when a star was actually removed, so repeated DELETEs
    // cannot drive the count below zero.
    const existing = await starRef.get();
    let changed = false;
    if (existing.exists) {
      await starRef.delete();
      await ctx.ref.update({ starCount: admin.firestore.FieldValue.increment(-1) });
      changed = true;
    }

    const snap = await ctx.ref.get();
    res.json({ starred: false, count: Math.max(0, (snap.data() || {}).starCount || 0), changed });
  } catch(err) {
    console.error('Unstar error:', err.message);
    res.status(500).json({ error: 'Server error' });
  }
});

app.get('/api/changelogs/:id/stars', async (req, res) => {
  try {
    const ctx = await changelogStarContext(req.params.id);
    if (ctx.error) return res.json({ count: 0 });
    const snap = await ctx.ref.get();
    res.json({ count: Math.max(0, (snap.data() || {}).starCount || 0) });
  } catch(err) {
    res.json({ count: 0 });
  }
});

app.get('/api/changelogs/:id/starred', async (req, res) => {
  const authHeader = req.headers.authorization || '';
  const idToken = authHeader.startsWith('Bearer ') ? authHeader.slice(7) : null;
  if (!idToken) return res.json({ starred: false });

  try {
    const decoded = await admin.auth().verifyIdToken(idToken);
    const uid = decoded.uid;
    if (!CHANGELOG_ID.test(String(req.params.id || ''))) return res.json({ starred: false });
    const snap = await db.collection(RELEASES).doc(req.params.id).collection('stars').doc(uid).get();
    res.json({ starred: snap.exists });
  } catch(err) {
    res.json({ starred: false });
  }
});

// ── FOLLOW / UNFOLLOW ──
app.post('/api/users/:uid/follow', async (req, res) => {
  const decoded = await authenticate(req, res);
  if (!decoded) return;

  const targetUid = String(req.params.uid || '');
  if (!targetUid || targetUid === 'undefined' || targetUid.length > 128 || targetUid.includes('/')) {
    return res.status(400).json({ error: 'Invalid user' });
  }
  if (decoded.uid === targetUid) return res.status(400).json({ error: 'Cannot follow yourself' });

  const limited = rateLimit('follow', decoded.uid, 200, 60 * 60 * 1000);
  if (!limited.allowed) return res.status(429).json({ error: 'Too many follow actions. Please try again later.' });

  try {
    // A creator can publish changelogs without ever saving a profile document,
    // so authorship also counts as proof that the account exists.
    const targetSnap = await db.collection('users').doc(targetUid).get();
    if (!targetSnap.exists) {
      const authored = await db.collection(RELEASES).where('uid', '==', targetUid).limit(1).get();
      if (!authored.size) return res.status(404).json({ error: 'User not found' });
    }

    const followRef = db.collection('users').doc(targetUid).collection('followers').doc(decoded.uid);
    const existing = await followRef.get();
    if (existing.exists) return res.json({ following: true, changed: false });

    await followRef.set({ createdAt: admin.firestore.FieldValue.serverTimestamp() });
    // Unified follows collection powers the personalized feed and follower lists.
    await db.collection('follows').doc(hub.followDocId(decoded.uid, 'user', targetUid)).set({
      followerUid: decoded.uid, targetType: 'user', targetId: targetUid,
      createdAt: admin.firestore.FieldValue.serverTimestamp(),
    });
    await db.collection('users').doc(targetUid).set({ followerCount: admin.firestore.FieldValue.increment(1) }, { merge: true }).catch(() => {});
    res.json({ following: true, changed: true });
  } catch(err) {
    console.error('Follow error:', err.message);
    res.status(500).json({ error: 'Server error' });
  }
});

app.delete('/api/users/:uid/follow', async (req, res) => {
  const authHeader = req.headers.authorization || '';
  const idToken = authHeader.startsWith('Bearer ') ? authHeader.slice(7) : null;
  if (!idToken) return res.status(401).json({ error: 'Unauthorized' });

  try {
    const decoded = await admin.auth().verifyIdToken(idToken);
    const followerUid = decoded.uid;
    await db.collection('users').doc(req.params.uid).collection('followers').doc(followerUid).delete();
    const unifiedRef = db.collection('follows').doc(hub.followDocId(followerUid, 'user', req.params.uid));
    const unifiedSnap = await unifiedRef.get();
    if (unifiedSnap.exists) {
      await unifiedRef.delete();
      await db.collection('users').doc(req.params.uid).set({ followerCount: admin.firestore.FieldValue.increment(-1) }, { merge: true }).catch(() => {});
    }
    res.json({ following: false, changed: true });
  } catch(err) {
    console.error('Unfollow error:', err.message);
    res.status(500).json({ error: 'Server error' });
  }
});

app.get('/api/users/:uid/followers/count', async (req, res) => {
  try {
    const snap = await db.collection('users').doc(req.params.uid).collection('followers').get();
    res.json({ count: snap.size });
  } catch(err) {
    res.json({ count: 0 });
  }
});

app.get('/api/users/:uid/following/check', async (req, res) => {
  const authHeader = req.headers.authorization || '';
  const idToken = authHeader.startsWith('Bearer ') ? authHeader.slice(7) : null;
  if (!idToken) return res.json({ following: false });

  try {
    const decoded = await admin.auth().verifyIdToken(idToken);
    const followerUid = decoded.uid;
    const snap = await db.collection('users').doc(req.params.uid).collection('followers').doc(followerUid).get();
    res.json({ following: snap.exists });
  } catch(err) {
    res.json({ following: false });
  }
});

// ── FEATURED / POPULAR ──
app.get('/api/featured', async (req, res) => {
  try {
    const snapshot = await db.collection('shared_changelogs').get();
    let items = [];
    for (const doc of snapshot.docs) {
      const data = doc.data();
      if (data.status === 'draft') continue; // drafts are private
      items.push({
        id: doc.id,
        title: data.title || 'Untitled',
        version: data.version || '',
        views: data.views || 0,
        stars: data.starCount || 0,
        author: data.authorName || 'Anonymous',
        authorId: data.uid,
        createdAt: data.createdAt?.toDate()?.toISOString() || null
      });
    }
    
    // Sort by stars desc, then views desc — top 12
    items.sort((a, b) => (b.stars || 0) - (a.stars || 0) || (b.views || 0) - (a.views || 0));
    res.json({ items: items.slice(0, 12) });
  } catch(err) {
    console.error('Featured error:', err.message);
    res.status(500).json({ error: 'Server error' });
  }
});

// ── RSS FEED ──
app.get('/rss/users/:uid', async (req, res) => {
  try {
    const uid = String(req.params.uid || '');
    const userSnap = await db.collection('users').doc(uid).get();
    const userData = userSnap.exists ? userSnap.data() : {};

    const snapshot = await db.collection('shared_changelogs')
      .where('uid', '==', uid)
      .get();

    let items = [];
    snapshot.forEach(doc => {
      const data = doc.data();
      if (data.status === 'draft') return; // drafts are private
      items.push({
        id: doc.id,
        slug: data.slug || null,
        author: data.authorName || '',
        title: data.title || 'Untitled',
        version: data.version || '',
        changelog: data.changelog || '',
        createdAt: data.createdAt?.toDate() || new Date()
      });
    });

    // Stay available for creators who never saved a profile document.
    if (!userSnap.exists && !items.length) return res.status(404).send('User not found');
    const name = userData.displayName || (items[0] && items[0].author) || 'Anonymous';

    items.sort((a, b) => b.createdAt - a.createdAt);
    items = items.slice(0, 50);

    const siteUrl = process.env.FRONTEND_URL || 'https://forkd-ai.com';
    const link = item => siteUrl + (item.slug ? '/changelog/' + item.slug : '/shared/' + item.id);
    
    res.set('Content-Type', 'application/rss+xml; charset=utf-8');
    res.send(`<?xml version="1.0" encoding="UTF-8"?>
<rss version="2.0" xmlns:atom="http://www.w3.org/2005/Atom">
<channel>
  <title>${escapeXml(name)} — Changelogs</title>
  <link>${siteUrl}/u/${uid}</link>
  <description>Latest changelogs from ${escapeXml(name)}</description>
  <language>en-us</language>
  <atom:link href="${siteUrl}/rss/users/${uid}" rel="self" type="application/rss+xml"/>
  ${items.map(item => `
  <item>
    <title>${escapeXml(item.title + (item.version ? ' ' + item.version : ''))}</title>
    <link>${link(item)}</link>
    <guid>${link(item)}</guid>
    <pubDate>${item.createdAt.toUTCString()}</pubDate>
    <description>${escapeXml(item.changelog.slice(0, 500))}</description>
  </item>`).join('')}
</channel>
</rss>`);
  } catch(err) {
    console.error('RSS error:', err.message);
    res.status(500).send('Server error');
  }
});

function escapeXml(s) {
  return String(s).replace(/&/g, '&amp;').replace(/</g, '&lt;').replace(/>/g, '&gt;').replace(/"/g, '&quot;');
}

// ═══════════════════════════════════════
// ── CHANGELOG HUB: PROJECTS + RELEASES ──
// ═══════════════════════════════════════
//
// Data model (adapts the existing collections rather than replacing them):
//   projects/{slug}                      a project, keyed by its readable URL slug
//   shared_changelogs/{id}               a release/changelog entry; may carry projectSlug
//   projects/{slug}/followers/{uid}      project followers (denormalized followerCount)
//   users/{uid}/followedProjects/{slug}  reader's follow index (avoids collection-group reads)
//   reports/{id}                         moderation reports
//
// `shared_changelogs` stays the single release store so existing /shared/:id
// links, stars, views and user feeds keep working unchanged.

const PROJECTS = 'projects';
const RELEASES = 'shared_changelogs';
const REPORT_REASONS = ['spam', 'incorrect', 'copyright', 'malicious', 'other'];

async function authenticate(req, res) {
  const authHeader = req.headers.authorization || '';
  const idToken = authHeader.startsWith('Bearer ') ? authHeader.slice(7) : null;
  if (!idToken) { res.status(401).json({ error: 'Unauthorized: no token provided' }); return null; }
  try {
    return await admin.auth().verifyIdToken(idToken);
  } catch (err) {
    console.error('Token verification failed:', err.message);
    res.status(401).json({ error: 'Unauthorized: invalid token' });
    return null;
  }
}

async function resolveAuthorName(decoded) {
  if (decoded.name) return decoded.name;
  try {
    const snap = await db.collection('users').doc(decoded.uid).get();
    const data = snap.exists ? snap.data() : {};
    if (data.displayName) return data.displayName;
  } catch (err) { /* fall through to email */ }
  if (decoded.email) return decoded.email.split('@')[0];
  return 'Anonymous';
}

function serializeProjectDoc(doc) {
  const data = doc.data() || {};
  const project = hub.serializeProject({ ...data, slug: doc.id });
  project.tagsText = (project.tags || []).join(' ');
  return project;
}

function serializeReleaseDoc(doc) {
  const release = hub.serializeRelease({ ...doc.data(), id: doc.id });
  release.tagsText = (release.tags || []).join(' ');
  return release;
}

async function loadProjects(limit = 500) {
  const snap = await db.collection(PROJECTS).limit(limit).get();
  const out = [];
  snap.forEach(doc => out.push(serializeProjectDoc(doc)));
  return out;
}

async function loadReleases(limit = 1000) {
  const snap = await db.collection(RELEASES).limit(limit).get();
  const out = [];
  snap.forEach(doc => out.push(serializeReleaseDoc(doc)));
  return out;
}

// Batch-fetch projects by slug. Firestore allows at most 10 values per `in`
// query, so the slug list is chunked instead of issuing one read per slug.
async function loadProjectsBySlugs(slugs) {
  const out = [];
  for (let i = 0; i < slugs.length; i += 10) {
    const chunk = slugs.slice(i, i + 10);
    const snap = await db.collection(PROJECTS)
      .where(admin.firestore.FieldPath.documentId(), 'in', chunk)
      .get();
    snap.forEach(doc => out.push(serializeProjectDoc(doc)));
  }
  return out;
}

// ── CATEGORIES ──
app.get('/api/categories', async (req, res) => {
  try {
    const projects = await loadProjects(1000);
    const counts = {};
    for (const project of projects) counts[project.category] = (counts[project.category] || 0) + 1;

    const releases = await db.collection(RELEASES).limit(1000).get();
    const changelogCounts = {};
    releases.forEach(doc => {
      const data = doc.data() || {};
      if (data.status === 'draft') return;
      const category = hub.normalizeCategory(data.category);
      changelogCounts[category] = (changelogCounts[category] || 0) + 1;
    });

    res.json({
      categories: hub.CATEGORIES.map(c => ({
        id: c.id,
        label: c.label,
        projectCount: counts[c.id] || 0,
        changelogCount: changelogCounts[c.id] || 0,
      }))
    });
  } catch (err) {
    console.error('Categories error:', err.message);
    res.status(500).json({ error: 'Server error' });
  }
});

// ── LIST PROJECTS ──
app.get('/api/projects', async (req, res) => {
  const { category, q, sort, cursor, limit } = req.query;
  try {
    const projects = await loadProjects(500);
    const filtered = hub.filterProjects(projects, {
      category: category || 'all',
      query: q || '',
      sort: sort === 'name' ? 'name' : 'recent',
    });
    const page = hub.paginate(filtered, { cursor, limit });
    res.json({ items: page.items, nextCursor: page.nextCursor, total: page.total });
  } catch (err) {
    console.error('Projects list error:', err.message);
    res.status(500).json({ error: 'Server error' });
  }
});

// ── PROJECTS THE CURRENT USER FOLLOWS (declared before /:slug) ──
app.get('/api/projects/following/mine', async (req, res) => {
  const decoded = await authenticate(req, res);
  if (!decoded) return;
  try {
    const followedSnap = await db.collection('users').doc(decoded.uid).collection('followedProjects').limit(200).get();
    const slugs = followedSnap.docs.map(doc => doc.id);
    if (!slugs.length) return res.json({ items: [] });

    const items = await loadProjectsBySlugs(slugs);
    res.json({ items: hub.sortByName(items) });
  } catch (err) {
    console.error('Followed projects error:', err.message);
    res.status(500).json({ error: 'Server error' });
  }
});

// ── PROJECTS THE CURRENT USER OWNS ──
app.get('/api/projects/mine', async (req, res) => {
  const decoded = await authenticate(req, res);
  if (!decoded) return;
  try {
    const projects = await loadProjects(500);
    const mine = projects.filter(p => p.ownerUid === decoded.uid);
    res.json({ items: hub.sortByName(mine) });
  } catch (err) {
    console.error('My projects error:', err.message);
    res.status(500).json({ error: 'Server error' });
  }
});

// ── GITHUB RELEASE IMPORT (read-only, reuses the existing GitHub integration) ──
// Lets a publisher prefill a release from an existing GitHub release instead of
// retyping it. Uses GITHUB_PAT when available for a higher rate limit.
app.get('/api/github/latest-release', async (req, res) => {
  const limited = rateLimit('github-import', clientKey(req), 30, 60 * 1000);
  if (!limited.allowed) return res.status(429).json({ error: 'Too many import attempts. Please wait a minute.' });

  const owner = String(req.query.owner || '').trim();
  const repo = String(req.query.repo || '').trim();
  const valid = v => /^[A-Za-z0-9._-]{1,100}$/.test(v);
  if (!valid(owner) || !valid(repo)) {
    return res.status(400).json({ error: 'Provide a valid GitHub owner and repository name' });
  }

  try {
    const headers = { 'Accept': 'application/vnd.github.v3+json', 'User-Agent': 'Forkd-AI' };
    if (process.env.GITHUB_PAT) headers.Authorization = `Bearer ${process.env.GITHUB_PAT}`;

    const ghRes = await fetch(`https://api.github.com/repos/${owner}/${repo}/releases/latest`, { headers });

    if (ghRes.status === 404) return res.status(404).json({ error: 'No releases found for that repository.' });
    if (ghRes.status === 403 || ghRes.status === 429) {
      return res.status(503).json({ error: 'GitHub rate limit reached. Try again later or publish the release manually.' });
    }
    if (!ghRes.ok) {
      return res.status(502).json({ error: 'GitHub request failed', status: ghRes.status });
    }

    const release = await ghRes.json();
    res.json({
      importSource: 'github',
      owner,
      repo,
      tag_name: release.tag_name || '',
      name: release.name || release.tag_name || '',
      body: release.body || '',
      published_at: release.published_at || null,
      html_url: release.html_url || '',
      author: release.author && release.author.login ? release.author.login : '',
    });
  } catch (err) {
    console.error('GitHub import error:', err.message);
    res.status(502).json({ error: 'Could not reach GitHub. Try again later.' });
  }
});

// ── GET ONE PROJECT + ITS RELEASES ──
app.get('/api/projects/:slug', async (req, res) => {
  const slug = hub.slugify(req.params.slug);
  if (!hub.isValidSlug(slug)) return res.status(400).json({ error: 'Invalid project slug' });
  try {
    const snap = await db.collection(PROJECTS).doc(slug).get();
    if (!snap.exists) return res.status(404).json({ error: 'Project not found' });
    const project = serializeProjectDoc(snap);

    const releaseSnap = await db.collection(RELEASES).where('projectSlug', '==', slug).limit(200).get();
    const releases = [];
    releaseSnap.forEach(doc => {
      const release = serializeReleaseDoc(doc);
      if (release.status === 'published') releases.push(release);
    });

    res.json({ project, releases: hub.sortByDateDesc(releases, 'createdAt') });
  } catch (err) {
    console.error('Project detail error:', err.message);
    res.status(500).json({ error: 'Server error' });
  }
});

// ── CREATE PROJECT ──
app.post('/api/projects', async (req, res) => {
  const decoded = await authenticate(req, res);
  if (!decoded) return;

  const check = hub.validateProjectInput(req.body);
  if (!check.ok) return res.status(400).json({ error: 'Invalid project', details: check.errors });

  const slug = hub.slugify(check.value.name);
  if (!hub.isValidSlug(slug)) {
    return res.status(400).json({ error: 'Could not build a valid URL from that name. Use at least two letters or numbers.' });
  }

  const limited = rateLimit('project-create', decoded.uid, 10, 60 * 60 * 1000);
  if (!limited.allowed) return res.status(429).json({ error: 'Too many projects created. Please try again later.' });

  try {
    const ref = db.collection(PROJECTS).doc(slug);
    const existing = await ref.get();
    if (existing.exists) {
      const data = existing.data() || {};
      const claimable = !data.claimed || !data.ownerUid;
      return res.status(409).json({
        error: 'slug_taken',
        slug,
        claimable,
        message: claimable
          ? 'A project page for this name already exists and can be claimed.'
          : 'A project page for this name already exists.',
      });
    }

    const now = admin.firestore.FieldValue.serverTimestamp();
    await ref.set({
      slug,
      name: check.value.name,
      description: check.value.description || '',
      category: check.value.category,
      tags: check.value.tags || [],
      websiteUrl: check.value.websiteUrl || '',
      repoUrl: check.value.repoUrl || '',
      logoUrl: check.value.logoUrl || '',
      sourceImport: check.value.sourceImport || '',
      ownerUid: decoded.uid,
      createdByUid: decoded.uid,
      claimed: true,
      claimedAt: now,
      claimMethod: 'creator',
      moderationStatus: 'active',
      releaseCount: 0,
      followerCount: 0,
      lastReleaseAt: null,
      createdAt: now,
      updatedAt: now,
    });

    console.log(`User ${decoded.uid} created project ${slug}`);
    res.status(201).json({ slug, url: `/p/${slug}`, claimable: false });
  } catch (err) {
    console.error('Create project error:', err.message);
    res.status(500).json({ error: 'Server error', detail: err.message });
  }
});

// ── EDIT PROJECT (owner only) ──
app.patch('/api/projects/:slug', async (req, res) => {
  const decoded = await authenticate(req, res);
  if (!decoded) return;
  const slug = hub.slugify(req.params.slug);
  if (!hub.isValidSlug(slug)) return res.status(400).json({ error: 'Invalid project slug' });

  const check = hub.validateProjectInput(req.body, { partial: true });
  if (!check.ok) return res.status(400).json({ error: 'Invalid project', details: check.errors });

  try {
    const ref = db.collection(PROJECTS).doc(slug);
    const snap = await ref.get();
    if (!snap.exists) return res.status(404).json({ error: 'Project not found' });
    const data = snap.data() || {};
    if (data.ownerUid !== decoded.uid) {
      return res.status(403).json({ error: 'Forbidden: you do not own this project' });
    }

    const update = {};
    for (const [key, value] of Object.entries(check.value)) {
      if (key === 'name' && !value) continue; // never blank out a name
      update[key] = value;
    }
    // The slug (public URL) is intentionally immutable so shared links stay stable.
    update.updatedAt = admin.firestore.FieldValue.serverTimestamp();
    await ref.set(update, { merge: true });

    res.json({ success: true, slug });
  } catch (err) {
    console.error('Edit project error:', err.message);
    res.status(500).json({ error: 'Server error' });
  }
});

// ── CLAIM AN EXISTING (UNCLAIMED) PROJECT ──
app.post('/api/projects/:slug/claim', async (req, res) => {
  const decoded = await authenticate(req, res);
  if (!decoded) return;

  const limited = rateLimit('project-claim', decoded.uid, 20, 60 * 60 * 1000);
  if (!limited.allowed) return res.status(429).json({ error: 'Too many claim attempts. Please try again later.' });

  const slug = hub.slugify(req.params.slug);
  if (!hub.isValidSlug(slug)) return res.status(400).json({ error: 'Invalid project slug' });
  const note = hub.cleanString(req.body && req.body.note, 300);

  try {
    const ref = db.collection(PROJECTS).doc(slug);
    const snap = await ref.get();
    if (!snap.exists) return res.status(404).json({ error: 'Project not found' });
    const data = snap.data() || {};

    if (data.claimed && data.ownerUid && data.ownerUid !== decoded.uid) {
      return res.status(403).json({ error: 'already_claimed', message: 'This project is already claimed by its owner.' });
    }

    const now = admin.firestore.FieldValue.serverTimestamp();
    await ref.set({
      ownerUid: decoded.uid,
      claimed: true,
      claimedAt: now,
      claimMethod: 'self-service',
      claimNote: note || '',
      updatedAt: now,
    }, { merge: true });

    console.log(`User ${decoded.uid} claimed project ${slug}`);
    res.json({ success: true, slug, claimable: false });
  } catch (err) {
    console.error('Claim project error:', err.message);
    res.status(500).json({ error: 'Server error' });
  }
});

// ── PUBLISH A RELEASE (owner only; explicit publish action) ──
app.post('/api/projects/:slug/releases', async (req, res) => {
  const decoded = await authenticate(req, res);
  if (!decoded) return;

  const slug = hub.slugify(req.params.slug);
  if (!hub.isValidSlug(slug)) return res.status(400).json({ error: 'Invalid project slug' });

  const check = hub.validateReleaseInput(req.body);
  if (!check.ok) return res.status(400).json({ error: 'Invalid release', details: check.errors });
  const value = check.value;

  const limited = rateLimit('release-publish', decoded.uid, 30, 60 * 60 * 1000);
  if (!limited.allowed) return res.status(429).json({ error: 'Publishing rate limit reached. Please try again later.' });

  try {
    const pref = db.collection(PROJECTS).doc(slug);
    const psnap = await pref.get();
    if (!psnap.exists) return res.status(404).json({ error: 'Project not found. Create or claim it first.' });
    const project = psnap.data() || {};
    if (project.ownerUid !== decoded.uid) {
      return res.status(403).json({ error: 'Forbidden', message: 'Claim this project before publishing releases to it.' });
    }

    // Duplicate detection: same version (or title) + source URL for this project.
    const existingSnap = await db.collection(RELEASES).where('projectSlug', '==', slug).limit(200).get();
    const existing = [];
    existingSnap.forEach(doc => existing.push({ id: doc.id, ...doc.data() }));
    const duplicate = hub.findDuplicateRelease(existing, slug, value);
    if (duplicate && !(req.body && req.body.force)) {
      return res.status(409).json({
        error: 'duplicate',
        message: 'A release with this version already exists for this project.',
        existingId: duplicate.id,
      });
    }

    const authorName = await resolveAuthorName(decoded);
    const releaseRef = db.collection(RELEASES).doc();
    const now = admin.firestore.FieldValue.serverTimestamp();
    const imported = value.source === 'github' && !!value.sourceUrl;

    await releaseRef.set({
      uid: decoded.uid,
      authorName,
      title: value.title,
      version: value.version,
      changelog: value.changelog,
      projectSlug: slug,
      projectName: project.name || slug,
      category: project.category || hub.DEFAULT_CATEGORY,
      tags: value.tags || [],
      source: value.source,
      sourceUrl: value.sourceUrl || '',
      sourceVerified: !imported,
      imported,
      generated: value.generated,
      status: value.status,
      views: 0,
      starCount: 0,
      createdAt: now,
      updatedAt: now,
    });

    if (value.status === 'published') {
      await pref.set({
        releaseCount: admin.firestore.FieldValue.increment(1),
        lastReleaseAt: now,
        updatedAt: now,
      }, { merge: true });
    }

    console.log(`User ${decoded.uid} published ${value.status} release ${value.version || value.title} on ${slug}`);
    res.status(201).json({
      id: releaseRef.id,
      url: `/shared/${releaseRef.id}`,
      status: value.status,
      imported,
      project: { slug, name: project.name || slug },
    });
  } catch (err) {
    console.error('Publish release error:', err.message);
    res.status(500).json({ error: 'Server error', detail: err.message });
  }
});

// ── FOLLOW / UNFOLLOW A PROJECT ──
app.post('/api/projects/:slug/follow', async (req, res) => {
  const decoded = await authenticate(req, res);
  if (!decoded) return;
  const slug = hub.slugify(req.params.slug);
  if (!hub.isValidSlug(slug)) return res.status(400).json({ error: 'Invalid project slug' });

  const limited = rateLimit('follow', decoded.uid, 120, 60 * 60 * 1000);
  if (!limited.allowed) return res.status(429).json({ error: 'Too many follow actions. Please try again later.' });

  try {
    const pref = db.collection(PROJECTS).doc(slug);
    const psnap = await pref.get();
    if (!psnap.exists) return res.status(404).json({ error: 'Project not found' });

    const followerRef = pref.collection('followers').doc(decoded.uid);
    const existing = await followerRef.get();
    if (existing.exists) return res.json({ following: true, changed: false });

    await followerRef.set({ createdAt: admin.firestore.FieldValue.serverTimestamp() });
    await db.collection('users').doc(decoded.uid).collection('followedProjects').doc(slug).set({
      slug,
      createdAt: admin.firestore.FieldValue.serverTimestamp(),
    });
    await pref.set({ followerCount: admin.firestore.FieldValue.increment(1) }, { merge: true }).catch(() => {});

    res.json({ following: true, changed: true });
  } catch (err) {
    console.error('Follow project error:', err.message);
    res.status(500).json({ error: 'Server error' });
  }
});

app.delete('/api/projects/:slug/follow', async (req, res) => {
  const decoded = await authenticate(req, res);
  if (!decoded) return;
  const slug = hub.slugify(req.params.slug);
  if (!hub.isValidSlug(slug)) return res.status(400).json({ error: 'Invalid project slug' });

  try {
    const pref = db.collection(PROJECTS).doc(slug);
    const followerRef = pref.collection('followers').doc(decoded.uid);
    const existing = await followerRef.get();
    if (existing.exists) {
      await followerRef.delete();
      await pref.set({ followerCount: admin.firestore.FieldValue.increment(-1) }, { merge: true }).catch(() => {});
    }
    await db.collection('users').doc(decoded.uid).collection('followedProjects').doc(slug).delete().catch(() => {});

    res.json({ following: false, changed: existing.exists });
  } catch (err) {
    console.error('Unfollow project error:', err.message);
    res.status(500).json({ error: 'Server error' });
  }
});

app.get('/api/projects/:slug/followers/count', async (req, res) => {
  const slug = hub.slugify(req.params.slug);
  if (!hub.isValidSlug(slug)) return res.status(400).json({ error: 'Invalid project slug' });
  try {
    const snap = await db.collection(PROJECTS).doc(slug).collection('followers').get();
    res.json({ count: snap.size });
  } catch (err) {
    res.json({ count: 0 });
  }
});

app.get('/api/projects/:slug/following/check', async (req, res) => {
  const authHeader = req.headers.authorization || '';
  const idToken = authHeader.startsWith('Bearer ') ? authHeader.slice(7) : null;
  if (!idToken) return res.json({ following: false });
  const slug = hub.slugify(req.params.slug);
  if (!hub.isValidSlug(slug)) return res.json({ following: false });
  try {
    const decoded = await admin.auth().verifyIdToken(idToken);
    const snap = await db.collection(PROJECTS).doc(slug).collection('followers').doc(decoded.uid).get();
    res.json({ following: snap.exists });
  } catch (err) {
    res.json({ following: false });
  }
});

// ── RECENT RELEASES FEED (public discovery) ──
app.get('/api/releases', async (req, res) => {
  const { category, q, source, cursor, limit, project } = req.query;
  try {
    const releases = await loadReleases(1000);
    const filtered = hub.filterReleases(releases, {
      category: category || 'all',
      query: q || '',
      source: source || 'all',
      projectSlug: project ? hub.slugify(project) : null,
    }).filter(r => r.status === 'published');

    const page = hub.paginate(filtered, { cursor, limit });
    res.json({ items: page.items, nextCursor: page.nextCursor, total: page.total });
  } catch (err) {
    console.error('Releases feed error:', err.message);
    res.status(500).json({ error: 'Server error' });
  }
});

app.get('/api/releases/:id', async (req, res) => {
  const id = String(req.params.id || '').replace(/[^a-zA-Z0-9_-]/g, '');
  if (!id) return res.status(400).json({ error: 'Invalid release id' });
  try {
    const snap = await db.collection(RELEASES).doc(id).get();
    if (!snap.exists) return res.status(404).json({ error: 'Release not found' });
    const release = serializeReleaseDoc(snap);

    // Drafts are private: only their author may read them.
    if (release.status === 'draft') {
      const authHeader = req.headers.authorization || '';
      const idToken = authHeader.startsWith('Bearer ') ? authHeader.slice(7) : null;
      let viewerUid = null;
      if (idToken) {
        try { viewerUid = (await admin.auth().verifyIdToken(idToken)).uid; } catch (err) { viewerUid = null; }
      }
      if (viewerUid !== snap.data().uid) return res.status(404).json({ error: 'Release not found' });
    }

    let project = null;
    if (release.projectSlug) {
      const psnap = await db.collection(PROJECTS).doc(release.projectSlug).get();
      if (psnap.exists) project = serializeProjectDoc(psnap);
    }
    res.json({ release, project });
  } catch (err) {
    console.error('Release detail error:', err.message);
    res.status(500).json({ error: 'Server error' });
  }
});

// ── FEED OF FOLLOWED PROJECTS (reader dashboard) ──
app.get('/api/feed/projects', async (req, res) => {
  const decoded = await authenticate(req, res);
  if (!decoded) return;
  try {
    const followedSnap = await db.collection('users').doc(decoded.uid).collection('followedProjects').limit(200).get();
    const slugs = followedSnap.docs.map(doc => doc.id);
    if (!slugs.length) return res.json({ items: [], followedProjects: 0, nextCursor: null });

    // Firestore `in` accepts at most 10 values, so chunk the slug list.
    const items = [];
    for (let i = 0; i < slugs.length; i += 10) {
      const chunk = slugs.slice(i, i + 10);
      const snap = await db.collection(RELEASES).where('projectSlug', 'in', chunk).limit(100).get();
      snap.forEach(doc => {
        const release = serializeReleaseDoc(doc);
        if (release.status === 'published') items.push(release);
      });
    }

    const page = hub.paginate(hub.sortByDateDesc(items, 'createdAt'), { cursor: req.query.cursor, limit: req.query.limit });
    res.json({ items: page.items, nextCursor: page.nextCursor, total: page.total, followedProjects: slugs.length });
  } catch (err) {
    console.error('Followed projects feed error:', err.message);
    res.status(500).json({ error: 'Server error' });
  }
});

// ── REPORT CONTENT (moderation) ──
app.post('/api/reports', async (req, res) => {
  const decoded = await authenticate(req, res);
  if (!decoded) return;

  const limited = rateLimit('report', decoded.uid, 10, 60 * 60 * 1000);
  if (!limited.allowed) return res.status(429).json({ error: 'Too many reports submitted. Please try again later.' });

  const targetType = req.body && req.body.targetType;
  const targetId = hub.cleanString(req.body && req.body.targetId, 120);
  const reason = hub.cleanString(req.body && req.body.reason, 40);
  const details = hub.cleanString(req.body && req.body.details, 1000);

  if (!['project', 'release'].includes(targetType)) {
    return res.status(400).json({ error: 'targetType must be "project" or "release"' });
  }
  if (!targetId) return res.status(400).json({ error: 'targetId is required' });
  if (!REPORT_REASONS.includes(reason)) {
    return res.status(400).json({ error: `reason must be one of: ${REPORT_REASONS.join(', ')}` });
  }

  try {
    await db.collection('reports').add({
      targetType,
      targetId,
      reason,
      details,
      reporterUid: decoded.uid,
      moderationStatus: 'open',
      createdAt: admin.firestore.FieldValue.serverTimestamp(),
    });
    res.status(202).json({ success: true, message: 'Report received. Thanks for helping keep Forkd clean.' });
  } catch (err) {
    console.error('Report error:', err.message);
    res.status(500).json({ error: 'Server error' });
  }
});

// ── NOTIFICATION PREFERENCES ──
// Persisted on the user document so the preference survives across devices.
// No transactional email provider is configured in this deployment, so these
// values are recorded but nothing is delivered yet — the UI says so explicitly.
function normalizeNotificationPrefs(data) {
  const source = (data && typeof data === 'object') ? data : {};
  return {
    notifyFollowedReleases: source.notifyFollowedReleases !== false,
    notifyOwnReleases: source.notifyOwnReleases !== false,
  };
}

app.get('/api/user/notification-preferences', async (req, res) => {
  const decoded = await authenticate(req, res);
  if (!decoded) return;
  try {
    const snap = await db.collection('users').doc(decoded.uid).get();
    const data = snap.exists ? snap.data() : {};
    res.json({ preferences: normalizeNotificationPrefs(data.notifications), emailDeliveryEnabled: false });
  } catch (err) {
    console.error('Notification prefs read error:', err.message);
    res.status(500).json({ error: 'Server error' });
  }
});

app.post('/api/user/notification-preferences', async (req, res) => {
  const decoded = await authenticate(req, res);
  if (!decoded) return;

  const input = (req.body && typeof req.body === 'object') ? req.body : {};
  const provided = ['notifyFollowedReleases', 'notifyOwnReleases'].filter(key => typeof input[key] === 'boolean');
  if (!provided.length) {
    return res.status(400).json({ error: 'Provide at least one boolean preference' });
  }

  try {
    const ref = db.collection('users').doc(decoded.uid);
    const snap = await ref.get();
    const current = normalizeNotificationPrefs(snap.exists ? snap.data().notifications : {});
    const next = { ...current };
    for (const key of provided) next[key] = input[key];

    await ref.set({ notifications: next, updatedAt: admin.firestore.FieldValue.serverTimestamp() }, { merge: true });
    res.json({ success: true, preferences: next, emailDeliveryEnabled: false });
  } catch (err) {
    console.error('Notification prefs write error:', err.message);
    res.status(500).json({ error: 'Server error' });
  }
});

// ═══════════════════════════════════════════════════════
// ── NETWORK: USERS, ORGANIZATIONS, FOLLOWS & CHANGELOGS ──
// ═══════════════════════════════════════════════════════
//
// Data model (added on top of the existing hub collections):
//   users/{uid}                                   creator profile (username, bio, links)
//   organizations/{slug}                          an organization / team page
//   organizations/{slug}/members/{uid}            membership + role (owner|admin|editor)
//   users/{uid}/organizations/{slug}              viewer's membership index
//   follows/{followerUid}__{type}__{targetId}     follows for users and organizations
//   shared_changelogs/{id}                        changelog entry (may carry orgSlug + slug)

const ORGANIZATIONS = 'organizations';
const FOLLOWS = 'follows';
const MEMBERS = 'members';

// ── Shared loaders ──
async function loadOrganizations(limit = 500) {
  const snap = await db.collection(ORGANIZATIONS).limit(limit).get();
  const out = [];
  snap.forEach(doc => out.push(hub.serializeOrganization({ ...doc.data(), slug: doc.id })));
  return out;
}

async function loadUsers(limit = 1000) {
  const snap = await db.collection('users').limit(limit).get();
  const out = [];
  snap.forEach(doc => out.push(hub.serializeUserProfile({ ...doc.data(), uid: doc.id })));
  return out;
}

async function getUserDoc(uid) {
  if (!uid) return {};
  const snap = await db.collection('users').doc(uid).get();
  return snap.exists ? snap.data() : {};
}

async function findUserByUsername(username) {
  const name = hub.normalizeUsername(username);
  if (!name) return null;
  const snap = await db.collection('users').where('username', '==', name).limit(1).get();
  if (!snap.docs.length) return null;
  const doc = snap.docs[0];
  return { uid: doc.id, ...doc.data() };
}

async function getMembership(slug, uid) {
  if (!slug || !uid) return null;
  const snap = await db.collection(ORGANIZATIONS).doc(slug).collection(MEMBERS).doc(uid).get();
  return snap.exists ? snap.data() : null;
}

async function serializeMembers(slug) {
  const snap = await db.collection(ORGANIZATIONS).doc(slug).collection(MEMBERS).limit(200).get();
  const members = [];
  snap.forEach(doc => members.push(hub.serializeMember({ ...doc.data(), uid: doc.id })));
  await Promise.all(members.map(async member => {
    if (!member.uid) return;
    try {
      const user = await getUserDoc(member.uid);
      member.username = user.username || member.username;
      member.displayName = user.displayName || member.displayName;
      member.photoURL = user.photoURL || member.photoURL;
    } catch (err) { /* keep the snapshot */ }
  }));
  const order = { owner: 0, admin: 1, editor: 2 };
  members.sort((a, b) => (order[a.role] - order[b.role]) || String(a.displayName).localeCompare(String(b.displayName)));
  return members;
}

async function loadChangelogs(limit = 1000) {
  const snap = await db.collection(RELEASES).limit(limit).get();
  const out = [];
  snap.forEach(doc => out.push(hub.serializeRelease({ ...doc.data(), id: doc.id })));
  return out;
}

async function loadPublicChangelogs(limit = 1000) {
  const items = await loadChangelogs(limit);
  return items.filter(c => c.status === 'published').map(c => ({ ...c, tagsText: (c.tags || []).join(' ') }));
}

async function optionalAuth(req) {
  const authHeader = req.headers.authorization || '';
  const idToken = authHeader.startsWith('Bearer ') ? authHeader.slice(7) : null;
  if (!idToken) return null;
  try { return await admin.auth().verifyIdToken(idToken); } catch (err) { return null; }
}

function memberSnapshot(target, role, joinedAt) {
  return {
    role,
    username: (target && target.username) || null,
    displayName: (target && (target.displayName || target.username)) || 'Member',
    photoURL: (target && target.photoURL) || null,
    joinedAt: joinedAt || admin.firestore.FieldValue.serverTimestamp(),
  };
}

async function writeMembershipIndex(uid, slug, role) {
  await db.collection('users').doc(uid).collection(ORGANIZATIONS).doc(slug).set({ slug, role }, { merge: true });
}

async function ensureUniqueChangelogSlug(base) {
  let candidate = base || 'changelog';
  for (let n = 2; n < 60; n += 1) {
    const snap = await db.collection(RELEASES).where('slug', '==', candidate).limit(1).get();
    if (!snap.docs.length) return candidate;
    candidate = `${base}-${n}`;
  }
  return `${base}-${Date.now().toString(36)}`;
}

async function publicUserSummary(uid) {
  const user = await getUserDoc(uid);
  return {
    uid,
    username: user.username || null,
    displayName: user.displayName || user.username || 'Anonymous',
    photoURL: user.photoURL || null,
  };
}

// ═══════════════════
// ── USER PROFILES ──
// ═══════════════════

// The signed-in user's own profile, including the organizations they belong to.
app.get('/api/user/profile', async (req, res) => {
  const decoded = await authenticate(req, res);
  if (!decoded) return;
  try {
    const data = await getUserDoc(decoded.uid);
    const profile = hub.serializeUserProfile({ ...data, uid: decoded.uid });
    if (!profile.displayName && decoded.name) profile.displayName = decoded.name;
    if (!profile.photoURL && decoded.picture) profile.photoURL = decoded.picture;

    const membershipSnap = await db.collection('users').doc(decoded.uid).collection(ORGANIZATIONS).limit(200).get();
    const organizations = [];
    membershipSnap.forEach(doc => organizations.push({ slug: doc.id, role: hub.normalizeRole(doc.data().role) || hub.DEFAULT_ORG_ROLE }));

    res.json({ profile, organizations });
  } catch (err) {
    console.error('Own profile error:', err.message);
    res.status(500).json({ error: 'Server error' });
  }
});

// Update the signed-in user's profile. Usernames are unique and validated.
app.post('/api/user/profile', async (req, res) => {
  const decoded = await authenticate(req, res);
  if (!decoded) return;

  const check = hub.validateProfileInput(req.body, { partial: true });
  if (!check.ok) return res.status(400).json({ error: 'Invalid profile', details: check.errors });
  const value = check.value;

  try {
    if (value.username) {
      const existing = await findUserByUsername(value.username);
      if (existing && existing.uid !== decoded.uid) {
        return res.status(409).json({ error: 'username_taken', message: 'That username is already taken.' });
      }
    }

    const update = { ...value };
    if (decoded.name && !update.displayName) update.displayName = decoded.name;
    if (decoded.picture && !update.photoURL) update.photoURL = decoded.picture;
    update.updatedAt = admin.firestore.FieldValue.serverTimestamp();

    const ref = db.collection('users').doc(decoded.uid);
    await ref.set(update, { merge: true });
    // Keep denormalized author names on the user's personal changelogs in sync.
    if (update.displayName || update.username) {
      const snap = await db.collection(RELEASES).where('uid', '==', decoded.uid).limit(200).get();
      await Promise.all(snap.docs.map(doc => {
        const data = doc.data() || {};
        if (data.authorType === 'organization') return null;
        const next = {};
        if (update.displayName) next.authorName = update.displayName;
        if (update.username) next.authorUsername = update.username;
        return Object.keys(next).length ? doc.ref.set(next, { merge: true }) : null;
      }));
    }

    const data = await getUserDoc(decoded.uid);
    res.json({ success: true, profile: hub.serializeUserProfile({ ...data, uid: decoded.uid }) });
  } catch (err) {
    console.error('Save profile error:', err.message);
    res.status(500).json({ error: 'Server error' });
  }
});

// Public profile by username, with real follower/changelog stats only.
app.get('/api/profiles/:username', async (req, res) => {
  const username = hub.normalizeUsername(req.params.username);
  if (!username) return res.status(400).json({ error: 'Invalid username' });
  try {
    const user = await findUserByUsername(username);
    if (!user) return res.status(404).json({ error: 'Profile not found' });

    const profile = hub.serializeUserProfile({ ...user, uid: user.uid });
    const viewer = await optionalAuth(req);

    const snap = await db.collection(RELEASES).where('uid', '==', user.uid).limit(200).get();
    const changelogs = [];
    snap.forEach(doc => {
      const release = hub.serializeRelease({ ...doc.data(), id: doc.id });
      if (release.status === 'published' && release.authorType !== 'organization') changelogs.push(release);
    });
    changelogs.sort((a, b) => hub.toMillis(b.publishedAt) - hub.toMillis(a.publishedAt));

    const followersSnap = await db.collection(FOLLOWS).where('targetType', '==', 'user').where('targetId', '==', user.uid).get();
    const followingSnap = await db.collection(FOLLOWS).where('followerUid', '==', user.uid).get();
    let isFollowing = false;
    if (viewer && viewer.uid !== user.uid) {
      const f = await db.collection(FOLLOWS)
        .where('followerUid', '==', viewer.uid).where('targetType', '==', 'user').where('targetId', '==', user.uid)
        .limit(1).get();
      isFollowing = f.size > 0;
    }

    const membershipSnap = await db.collection('users').doc(user.uid).collection(ORGANIZATIONS).limit(50).get();
    const organizations = [];
    for (const doc of membershipSnap.docs) {
      const osnap = await db.collection(ORGANIZATIONS).doc(doc.id).get();
      if (!osnap.exists) continue;
      const org = hub.serializeOrganization({ ...osnap.data(), slug: doc.id });
      organizations.push({ slug: org.slug, name: org.name, avatarUrl: org.avatarUrl, role: hub.normalizeRole(doc.data().role) || hub.DEFAULT_ORG_ROLE });
    }

    let totalViews = 0;
    let totalStars = 0;
    for (const c of changelogs) { totalViews += c.views || 0; totalStars += c.stars || 0; }

    res.json({
      profile,
      stats: {
        publicChangelogs: changelogs.length,
        totalViews,
        totalStars,
        followers: followersSnap.size,
        following: followingSnap.size,
        organizations: organizations.length,
      },
      isFollowing,
      changelogs,
      organizations,
    });
  } catch (err) {
    console.error('Public profile error:', err.message);
    res.status(500).json({ error: 'Server error' });
  }
});

// ══════════════════════════════
// ── ORGANIZATIONS & MEMBERS ──
// ══════════════════════════════

app.get('/api/orgs', async (req, res) => {
  const { q, category, sort, cursor, limit } = req.query;
  try {
    let orgs = await loadOrganizations(500);
    if (category && category !== 'all') orgs = orgs.filter(o => o.category === category);
    if (q) orgs = orgs.filter(o => hub.matchesQuery(o, q, ['name', 'description', 'tags']));
    if (sort === 'name') orgs = hub.sortByName(orgs);
    else if (sort === 'followers') orgs.sort((a, b) => (b.followerCount || 0) - (a.followerCount || 0));
    else orgs = hub.sortByDateDesc(orgs, 'createdAt');
    const page = hub.paginate(orgs, { cursor, limit });
    res.json({ items: page.items, nextCursor: page.nextCursor, total: page.total });
  } catch (err) {
    console.error('Organizations list error:', err.message);
    res.status(500).json({ error: 'Server error' });
  }
});

app.get('/api/orgs/mine', async (req, res) => {
  const decoded = await authenticate(req, res);
  if (!decoded) return;
  try {
    const snap = await db.collection('users').doc(decoded.uid).collection(ORGANIZATIONS).limit(200).get();
    const items = [];
    for (const doc of snap.docs) {
      const osnap = await db.collection(ORGANIZATIONS).doc(doc.id).get();
      if (!osnap.exists) continue;
      const org = hub.serializeOrganization({ ...osnap.data(), slug: doc.id });
      items.push({ ...org, role: hub.normalizeRole(doc.data().role) || hub.DEFAULT_ORG_ROLE });
    }
    items.sort((a, b) => String(a.name).localeCompare(String(b.name)));
    res.json({ items });
  } catch (err) {
    console.error('My organizations error:', err.message);
    res.status(500).json({ error: 'Server error' });
  }
});

app.get('/api/orgs/following/mine', async (req, res) => {
  const decoded = await authenticate(req, res);
  if (!decoded) return;
  try {
    const snap = await db.collection(FOLLOWS).where('followerUid', '==', decoded.uid).where('targetType', '==', 'organization').limit(200).get();
    const items = [];
    for (const doc of snap.docs) {
      const osnap = await db.collection(ORGANIZATIONS).doc(doc.data().targetId).get();
      if (osnap.exists) items.push(hub.serializeOrganization({ ...osnap.data(), slug: osnap.id }));
    }
    res.json({ items: hub.sortByName(items) });
  } catch (err) {
    console.error('Followed organizations error:', err.message);
    res.status(500).json({ error: 'Server error' });
  }
});

app.post('/api/orgs', async (req, res) => {
  const decoded = await authenticate(req, res);
  if (!decoded) return;

  const check = hub.validateOrgInput(req.body);
  if (!check.ok) return res.status(400).json({ error: 'Invalid organization', details: check.errors });

  const slug = hub.slugify(check.value.name);
  if (!hub.isValidSlug(slug)) {
    return res.status(400).json({ error: 'Could not build a valid URL from that name. Use at least two letters or numbers.' });
  }

  const limited = rateLimit('org-create', decoded.uid, 10, 60 * 60 * 1000);
  if (!limited.allowed) return res.status(429).json({ error: 'Too many organizations created. Please try again later.' });

  try {
    const ref = db.collection(ORGANIZATIONS).doc(slug);
    const existing = await ref.get();
    if (existing.exists) return res.status(409).json({ error: 'slug_taken', slug, message: 'An organization already uses this name.' });

    const user = await getUserDoc(decoded.uid);
    const now = admin.firestore.FieldValue.serverTimestamp();
    await ref.set({
      slug,
      name: check.value.name,
      description: check.value.description || '',
      category: check.value.category,
      tags: check.value.tags || [],
      websiteUrl: check.value.websiteUrl || '',
      avatarUrl: check.value.avatarUrl || '',
      ownerUid: decoded.uid,
      memberCount: 1,
      followerCount: 0,
      changelogCount: 0,
      lastReleaseAt: null,
      createdAt: now,
      updatedAt: now,
    });
    await ref.collection(MEMBERS).doc(decoded.uid).set(memberSnapshot({ ...user, displayName: user.displayName || decoded.name, photoURL: user.photoURL || decoded.picture }, 'owner', now));
    await writeMembershipIndex(decoded.uid, slug, 'owner');

    console.log(`User ${decoded.uid} created organization ${slug}`);
    res.status(201).json({ slug, url: `/org/${slug}`, role: 'owner' });
  } catch (err) {
    console.error('Create organization error:', err.message);
    res.status(500).json({ error: 'Server error', detail: err.message });
  }
});

app.get('/api/orgs/:slug', async (req, res) => {
  const slug = hub.slugify(req.params.slug);
  if (!hub.isValidSlug(slug)) return res.status(400).json({ error: 'Invalid organization slug' });
  try {
    const snap = await db.collection(ORGANIZATIONS).doc(slug).get();
    if (!snap.exists) return res.status(404).json({ error: 'Organization not found' });
    const organization = hub.serializeOrganization({ ...snap.data(), slug });
    const members = await serializeMembers(slug);

    const viewer = await optionalAuth(req);
    let viewerRole = null;
    let isFollowing = false;
    if (viewer) {
      const membership = await getMembership(slug, viewer.uid);
      viewerRole = membership ? (hub.normalizeRole(membership.role) || hub.DEFAULT_ORG_ROLE) : null;
      const f = await db.collection(FOLLOWS).where('followerUid', '==', viewer.uid).where('targetType', '==', 'organization').where('targetId', '==', slug).limit(1).get();
      isFollowing = f.size > 0;
    }

    const changelogSnap = await db.collection(RELEASES).where('orgSlug', '==', slug).limit(200).get();
    const changelogs = [];
    changelogSnap.forEach(doc => {
      const release = hub.serializeRelease({ ...doc.data(), id: doc.id });
      if (release.status === 'published') changelogs.push(release);
    });
    changelogs.sort((a, b) => hub.toMillis(b.publishedAt) - hub.toMillis(a.publishedAt));

    res.json({ organization, members, changelogs, viewerRole, isFollowing });
  } catch (err) {
    console.error('Organization detail error:', err.message);
    res.status(500).json({ error: 'Server error' });
  }
});

app.patch('/api/orgs/:slug', async (req, res) => {
  const decoded = await authenticate(req, res);
  if (!decoded) return;
  const slug = hub.slugify(req.params.slug);
  if (!hub.isValidSlug(slug)) return res.status(400).json({ error: 'Invalid organization slug' });

  const check = hub.validateOrgInput(req.body, { partial: true });
  if (!check.ok) return res.status(400).json({ error: 'Invalid organization', details: check.errors });

  try {
    const ref = db.collection(ORGANIZATIONS).doc(slug);
    const snap = await ref.get();
    if (!snap.exists) return res.status(404).json({ error: 'Organization not found' });
    const membership = await getMembership(slug, decoded.uid);
    if (!membership || !hub.canEditOrganization(hub.normalizeRole(membership.role))) {
      return res.status(403).json({ error: 'Forbidden', message: 'Only the organization owner can change its settings.' });
    }

    const update = {};
    for (const [key, val] of Object.entries(check.value)) {
      if (key === 'name' && !val) continue;
      update[key] = val;
    }
    update.updatedAt = admin.firestore.FieldValue.serverTimestamp();
    await ref.set(update, { merge: true });
    res.json({ success: true, slug });
  } catch (err) {
    console.error('Edit organization error:', err.message);
    res.status(500).json({ error: 'Server error' });
  }
});

app.delete('/api/orgs/:slug', async (req, res) => {
  const decoded = await authenticate(req, res);
  if (!decoded) return;
  const slug = hub.slugify(req.params.slug);
  if (!hub.isValidSlug(slug)) return res.status(400).json({ error: 'Invalid organization slug' });
  try {
    const ref = db.collection(ORGANIZATIONS).doc(slug);
    const snap = await ref.get();
    if (!snap.exists) return res.status(404).json({ error: 'Organization not found' });
    const membership = await getMembership(slug, decoded.uid);
    if (!membership || !hub.canDeleteOrganization(hub.normalizeRole(membership.role))) {
      return res.status(403).json({ error: 'Forbidden', message: 'Only the organization owner can delete it.' });
    }

    const memberSnap = await ref.collection(MEMBERS).limit(200).get();
    await Promise.all(memberSnap.docs.map(doc => db.collection('users').doc(doc.id).collection(ORGANIZATIONS).doc(slug).delete().catch(() => {})));
    await Promise.all(memberSnap.docs.map(doc => doc.ref.delete()));
    await ref.delete();

    console.log(`Organization ${slug} deleted by ${decoded.uid}`);
    res.json({ success: true });
  } catch (err) {
    console.error('Delete organization error:', err.message);
    res.status(500).json({ error: 'Server error' });
  }
});

app.get('/api/orgs/:slug/members', async (req, res) => {
  const slug = hub.slugify(req.params.slug);
  if (!hub.isValidSlug(slug)) return res.status(400).json({ error: 'Invalid organization slug' });
  try {
    const snap = await db.collection(ORGANIZATIONS).doc(slug).get();
    if (!snap.exists) return res.status(404).json({ error: 'Organization not found' });
    res.json({ items: await serializeMembers(slug) });
  } catch (err) {
    console.error('Organization members error:', err.message);
    res.status(500).json({ error: 'Server error' });
  }
});

app.post('/api/orgs/:slug/members', async (req, res) => {
  const decoded = await authenticate(req, res);
  if (!decoded) return;
  const slug = hub.slugify(req.params.slug);
  if (!hub.isValidSlug(slug)) return res.status(400).json({ error: 'Invalid organization slug' });

  try {
    const ref = db.collection(ORGANIZATIONS).doc(slug);
    const snap = await ref.get();
    if (!snap.exists) return res.status(404).json({ error: 'Organization not found' });
    const membership = await getMembership(slug, decoded.uid);
    const actorRole = membership ? hub.normalizeRole(membership.role) : null;
    if (!actorRole || !hub.canManageMembers(actorRole)) {
      return res.status(403).json({ error: 'Forbidden', message: 'Only owners and admins can manage members.' });
    }

    const body = (req.body && typeof req.body === 'object') ? req.body : {};
    const uidInput = hub.cleanString(body.uid, 80);
    let target = null;
    if (uidInput) {
      const usnap = await db.collection('users').doc(uidInput).get();
      if (usnap.exists) target = { uid: uidInput, ...usnap.data() };
    } else {
      target = await findUserByUsername(body.username);
    }
    if (!target) return res.status(404).json({ error: 'user_not_found', message: 'That user needs a Forkd account first.' });

    const role = hub.normalizeRole(body.role) || hub.DEFAULT_ORG_ROLE;
    if (role === 'owner') return res.status(400).json({ error: 'Use role change to transfer ownership.' });
    if (!hub.canManageMember(actorRole, role)) {
      return res.status(403).json({ error: 'Forbidden', message: 'You cannot grant a role equal to or above your own.' });
    }

    const memberRef = ref.collection(MEMBERS).doc(target.uid);
    const existingMember = await memberRef.get();
    if (existingMember.exists) return res.status(409).json({ error: 'already_member' });

    await memberRef.set(memberSnapshot(target, role));
    await writeMembershipIndex(target.uid, slug, role);
    await ref.set({ memberCount: admin.firestore.FieldValue.increment(1), updatedAt: admin.firestore.FieldValue.serverTimestamp() }, { merge: true });

    res.status(201).json({ success: true, member: hub.serializeMember({ ...(await memberRef.get()).data(), uid: target.uid }) });
  } catch (err) {
    console.error('Add member error:', err.message);
    res.status(500).json({ error: 'Server error' });
  }
});

app.patch('/api/orgs/:slug/members/:uid', async (req, res) => {
  const decoded = await authenticate(req, res);
  if (!decoded) return;
  const slug = hub.slugify(req.params.slug);
  if (!hub.isValidSlug(slug)) return res.status(400).json({ error: 'Invalid organization slug' });
  const targetUid = hub.cleanString(req.params.uid, 80);
  if (!targetUid) return res.status(400).json({ error: 'Invalid member id' });

  const newRole = hub.normalizeRole((req.body || {}).role);
  if (!newRole) return res.status(400).json({ error: 'role must be owner, admin or editor' });

  try {
    const ref = db.collection(ORGANIZATIONS).doc(slug);
    const snap = await ref.get();
    if (!snap.exists) return res.status(404).json({ error: 'Organization not found' });
    const membership = await getMembership(slug, decoded.uid);
    const actorRole = membership ? hub.normalizeRole(membership.role) : null;
    if (!actorRole || !hub.canManageMembers(actorRole)) {
      return res.status(403).json({ error: 'Forbidden', message: 'Only owners and admins can manage members.' });
    }

    const memberRef = ref.collection(MEMBERS).doc(targetUid);
    const memberSnap = await memberRef.get();
    if (!memberSnap.exists) return res.status(404).json({ error: 'Member not found' });
    const targetRole = hub.normalizeRole(memberSnap.data().role) || hub.DEFAULT_ORG_ROLE;

    const update = { role: newRole };
    if (newRole === 'owner') {
      if (actorRole !== 'owner') return res.status(403).json({ error: 'Forbidden', message: 'Only the owner can transfer ownership.' });
      if (targetUid === decoded.uid) return res.status(400).json({ error: 'You are already the owner.' });
      // Ownership transfer: the previous owner steps down to admin.
      await ref.collection(MEMBERS).doc(decoded.uid).set({ role: 'admin' }, { merge: true });
      await writeMembershipIndex(decoded.uid, slug, 'admin');
      update.ownerUid = targetUid;
    } else {
      if (!hub.canManageMember(actorRole, targetRole)) {
        return res.status(403).json({ error: 'Forbidden', message: 'You cannot change a member at or above your role.' });
      }
      if (!hub.canManageMember(actorRole, newRole)) {
        return res.status(403).json({ error: 'Forbidden', message: 'You cannot grant a role equal to or above your own.' });
      }
      if (targetUid === decoded.uid) return res.status(400).json({ error: 'You cannot change your own role.' });
    }

    await memberRef.set({ role: newRole, updatedAt: admin.firestore.FieldValue.serverTimestamp() }, { merge: true });
    await writeMembershipIndex(targetUid, slug, newRole);
    if (update.ownerUid) {
      await ref.set({ ownerUid: update.ownerUid, updatedAt: admin.firestore.FieldValue.serverTimestamp() }, { merge: true });
    }

    res.json({ success: true, uid: targetUid, role: newRole });
  } catch (err) {
    console.error('Change member role error:', err.message);
    res.status(500).json({ error: 'Server error' });
  }
});

app.delete('/api/orgs/:slug/members/:uid', async (req, res) => {
  const decoded = await authenticate(req, res);
  if (!decoded) return;
  const slug = hub.slugify(req.params.slug);
  if (!hub.isValidSlug(slug)) return res.status(400).json({ error: 'Invalid organization slug' });
  const targetUid = hub.cleanString(req.params.uid, 80);
  if (!targetUid) return res.status(400).json({ error: 'Invalid member id' });

  try {
    const ref = db.collection(ORGANIZATIONS).doc(slug);
    const snap = await ref.get();
    if (!snap.exists) return res.status(404).json({ error: 'Organization not found' });
    const membership = await getMembership(slug, decoded.uid);
    const actorRole = membership ? hub.normalizeRole(membership.role) : null;
    if (!actorRole || !hub.canManageMembers(actorRole)) {
      return res.status(403).json({ error: 'Forbidden', message: 'Only owners and admins can manage members.' });
    }

    const memberRef = ref.collection(MEMBERS).doc(targetUid);
    const memberSnap = await memberRef.get();
    if (!memberSnap.exists) return res.status(404).json({ error: 'Member not found' });
    const targetRole = hub.normalizeRole(memberSnap.data().role) || hub.DEFAULT_ORG_ROLE;
    if (!hub.canManageMember(actorRole, targetRole)) {
      return res.status(403).json({ error: 'Forbidden', message: 'You cannot remove the organization owner or a member at your own role.' });
    }

    await memberRef.delete();
    await db.collection('users').doc(targetUid).collection(ORGANIZATIONS).doc(slug).delete().catch(() => {});
    await ref.set({ memberCount: admin.firestore.FieldValue.increment(-1), updatedAt: admin.firestore.FieldValue.serverTimestamp() }, { merge: true });

    res.json({ success: true });
  } catch (err) {
    console.error('Remove member error:', err.message);
    res.status(500).json({ error: 'Server error' });
  }
});

// ── FOLLOW AN ORGANIZATION ──
app.post('/api/orgs/:slug/follow', async (req, res) => {
  const decoded = await authenticate(req, res);
  if (!decoded) return;
  const slug = hub.slugify(req.params.slug);
  if (!hub.isValidSlug(slug)) return res.status(400).json({ error: 'Invalid organization slug' });
  const limited = rateLimit('follow', decoded.uid, 200, 60 * 60 * 1000);
  if (!limited.allowed) return res.status(429).json({ error: 'Too many follow actions. Please try again later.' });
  try {
    const ref = db.collection(ORGANIZATIONS).doc(slug);
    const snap = await ref.get();
    if (!snap.exists) return res.status(404).json({ error: 'Organization not found' });

    const followRef = db.collection(FOLLOWS).doc(hub.followDocId(decoded.uid, 'organization', slug));
    const existing = await followRef.get();
    if (existing.exists) return res.json({ following: true, changed: false });

    await followRef.set({ followerUid: decoded.uid, targetType: 'organization', targetId: slug, createdAt: admin.firestore.FieldValue.serverTimestamp() });
    await ref.set({ followerCount: admin.firestore.FieldValue.increment(1) }, { merge: true }).catch(() => {});
    res.json({ following: true, changed: true });
  } catch (err) {
    console.error('Follow organization error:', err.message);
    res.status(500).json({ error: 'Server error' });
  }
});

app.delete('/api/orgs/:slug/follow', async (req, res) => {
  const decoded = await authenticate(req, res);
  if (!decoded) return;
  const slug = hub.slugify(req.params.slug);
  if (!hub.isValidSlug(slug)) return res.status(400).json({ error: 'Invalid organization slug' });
  try {
    const followRef = db.collection(FOLLOWS).doc(hub.followDocId(decoded.uid, 'organization', slug));
    const existing = await followRef.get();
    if (existing.exists) {
      await followRef.delete();
      await db.collection(ORGANIZATIONS).doc(slug).set({ followerCount: admin.firestore.FieldValue.increment(-1) }, { merge: true }).catch(() => {});
    }
    res.json({ following: false, changed: existing.exists });
  } catch (err) {
    console.error('Unfollow organization error:', err.message);
    res.status(500).json({ error: 'Server error' });
  }
});

app.get('/api/orgs/:slug/followers/count', async (req, res) => {
  const slug = hub.slugify(req.params.slug);
  if (!hub.isValidSlug(slug)) return res.status(400).json({ error: 'Invalid organization slug' });
  try {
    const snap = await db.collection(FOLLOWS).where('targetType', '==', 'organization').where('targetId', '==', slug).get();
    res.json({ count: snap.size });
  } catch (err) {
    res.json({ count: 0 });
  }
});

app.get('/api/orgs/:slug/following/check', async (req, res) => {
  const viewer = await optionalAuth(req);
  const slug = hub.slugify(req.params.slug);
  if (!viewer || !hub.isValidSlug(slug)) return res.json({ following: false });
  try {
    const snap = await db.collection(FOLLOWS).doc(hub.followDocId(viewer.uid, 'organization', slug)).get();
    res.json({ following: snap.exists });
  } catch (err) {
    res.json({ following: false });
  }
});

app.get('/api/orgs/:slug/followers', async (req, res) => {
  const slug = hub.slugify(req.params.slug);
  if (!hub.isValidSlug(slug)) return res.status(400).json({ error: 'Invalid organization slug' });
  try {
    const snap = await db.collection(FOLLOWS).where('targetType', '==', 'organization').where('targetId', '==', slug).limit(200).get();
    const items = [];
    for (const doc of snap.docs) items.push(await publicUserSummary(doc.data().followerUid));
    res.json({ items });
  } catch (err) {
    console.error('Organization followers error:', err.message);
    res.status(500).json({ error: 'Server error' });
  }
});

// ═══════════════════════
// ── CHANGELOGS ──
// ═══════════════════════

// Publish a changelog as an individual or as an organization.
app.post('/api/changelogs', async (req, res) => {
  const decoded = await authenticate(req, res);
  if (!decoded) return;

  const input = (req.body && typeof req.body === 'object') ? req.body : {};
  const check = hub.validateReleaseInput(input);
  if (!check.ok) return res.status(400).json({ error: 'Invalid changelog', details: check.errors });
  const value = check.value;

  const limited = rateLimit('changelog-publish', decoded.uid, 60, 60 * 60 * 1000);
  if (!limited.allowed) return res.status(429).json({ error: 'Publishing rate limit reached. Please try again later.' });

  const orgSlug = input.orgSlug ? hub.slugify(input.orgSlug) : '';
  const projectSlug = input.projectSlug ? hub.slugify(input.projectSlug) : '';
  if (input.orgSlug && !hub.isValidSlug(orgSlug)) return res.status(400).json({ error: 'Invalid organization slug' });

  try {
    const user = await getUserDoc(decoded.uid);
    let ownerName = user.displayName || decoded.name || 'Anonymous';
    let authorName = ownerName;
    let authorType = 'user';
    let orgRef = null;
    let orgData = null;
    let projectData = null;

    if (orgSlug) {
      const osnap = await db.collection(ORGANIZATIONS).doc(orgSlug).get();
      if (!osnap.exists) return res.status(404).json({ error: 'Organization not found' });
      orgData = osnap.data();
      orgRef = osnap.ref;
      const membership = await getMembership(orgSlug, decoded.uid);
      if (!membership || !hub.canCreateChangelog(hub.normalizeRole(membership.role))) {
        return res.status(403).json({ error: 'Forbidden', message: 'You must be an editor of this organization to publish its changelogs.' });
      }
      ownerName = orgData.name || orgSlug;
      authorName = orgData.name || orgSlug;
      authorType = 'organization';
    }

    if (projectSlug) {
      const psnap = await db.collection(PROJECTS).doc(projectSlug).get();
      if (!psnap.exists) return res.status(404).json({ error: 'Project not found' });
      projectData = psnap.data();
      if (orgSlug && projectData.orgSlug !== orgSlug) {
        return res.status(400).json({ error: 'The selected project does not belong to this organization.' });
      }
      const ownsProject = projectData.ownerUid === decoded.uid;
      const orgMembership = projectData.orgSlug ? await getMembership(projectData.orgSlug, decoded.uid) : null;
      if (!ownsProject && !(orgMembership && hub.canCreateChangelog(hub.normalizeRole(orgMembership.role)))) {
        return res.status(403).json({ error: 'Forbidden', message: 'Claim this project before publishing releases to it.' });
      }
    }

    // Duplicate detection within the owning scope (user, org or project).
    const scopeQuery = orgSlug
      ? db.collection(RELEASES).where('orgSlug', '==', orgSlug)
      : db.collection(RELEASES).where('uid', '==', decoded.uid);
    const existingSnap = await scopeQuery.limit(200).get();
    const existing = [];
    existingSnap.forEach(doc => existing.push({ id: doc.id, ...doc.data() }));
    const duplicate = hub.findDuplicateRelease(existing, orgSlug || decoded.uid, value);
    if (duplicate && !(req.body && req.body.force)) {
      return res.status(409).json({ error: 'duplicate', message: 'You already published a changelog with this version.', existingId: duplicate.id });
    }

    const slug = await ensureUniqueChangelogSlug(hub.buildChangelogSlug({ ownerName, version: value.version, title: value.title }));
    const now = admin.firestore.FieldValue.serverTimestamp();
    const ref = db.collection(RELEASES).doc();

    await ref.set({
      uid: decoded.uid,
      authorName,
      authorUsername: user.username || null,
      authorType,
      title: value.title,
      version: value.version,
      slug,
      changelog: value.changelog,
      orgSlug: orgSlug || '',
      orgName: orgData ? (orgData.name || orgSlug) : '',
      projectSlug: projectSlug || '',
      projectName: projectData ? (projectData.name || projectSlug) : '',
      category: value.category || (orgData && orgData.category) || (projectData && projectData.category) || hub.DEFAULT_CATEGORY,
      tags: value.tags || [],
      source: value.source,
      sourceUrl: value.sourceUrl || '',
      visibility: value.visibility,
      sourceVerified: value.source !== 'github',
      imported: value.source === 'github' && !!value.sourceUrl,
      generated: value.generated,
      status: value.status,
      views: 0,
      starCount: 0,
      createdAt: now,
      updatedAt: now,
      publishedAt: value.status === 'published' ? now : null,
    });

    if (value.status === 'published') {
      if (orgRef) await orgRef.set({ changelogCount: admin.firestore.FieldValue.increment(1), lastReleaseAt: now, updatedAt: now }, { merge: true });
      if (projectSlug) await db.collection(PROJECTS).doc(projectSlug).set({ releaseCount: admin.firestore.FieldValue.increment(1), lastReleaseAt: now, updatedAt: now }, { merge: true });
    }

    console.log(`User ${decoded.uid} published changelog ${slug} (${authorType})`);
    res.status(201).json({ id: ref.id, slug, url: `/changelog/${slug}`, status: value.status, orgSlug: orgSlug || null });
  } catch (err) {
    console.error('Publish changelog error:', err.message);
    res.status(500).json({ error: 'Server error', detail: err.message });
  }
});

app.get('/api/changelogs/mine', async (req, res) => {
  const decoded = await authenticate(req, res);
  if (!decoded) return;
  try {
    const snapshot = await db.collection(RELEASES).where('uid', '==', decoded.uid).limit(200).get();
    const items = [];
    snapshot.forEach(doc => items.push(hub.serializeRelease({ ...doc.data(), id: doc.id })));
    res.json({ items: hub.sortByDateDesc(items, 'createdAt') });
  } catch (err) {
    console.error('My changelogs error:', err.message);
    res.status(500).json({ error: 'Server error' });
  }
});

app.get('/api/changelogs', async (req, res) => {
  const { q, category, tag, org, author, from, to, cursor, limit } = req.query;
  try {
    const all = await loadPublicChangelogs(1000);
    const filtered = hub.filterChangelogs(all, {
      query: q || '',
      category: category || 'all',
      tag: tag || '',
      orgSlug: org ? hub.slugify(org) : '',
      authorUsername: author ? hub.normalizeUsername(author) : '',
      from: from || '',
      to: to || '',
    });
    const page = hub.paginate(filtered, { cursor, limit });
    res.json({ items: page.items, nextCursor: page.nextCursor, total: page.total });
  } catch (err) {
    console.error('Changelogs list error:', err.message);
    res.status(500).json({ error: 'Server error' });
  }
});

app.get('/api/changelogs/:idOrSlug', async (req, res) => {
  const key = String(req.params.idOrSlug || '').trim();
  if (!key || !/^[a-zA-Z0-9_-]{1,80}$/.test(key)) return res.status(400).json({ error: 'Invalid changelog id' });
  try {
    let snap = await db.collection(RELEASES).doc(key).get();
    let release = snap.exists ? hub.serializeRelease({ ...snap.data(), id: snap.id }) : null;
    if (!release) {
      const bySlug = await db.collection(RELEASES).where('slug', '==', key.toLowerCase()).limit(1).get();
      if (bySlug.docs.length) {
        snap = bySlug.docs[0];
        release = hub.serializeRelease({ ...snap.data(), id: snap.id });
      }
    }
    if (!release) return res.status(404).json({ error: 'Changelog not found' });
    if (release.visibility === 'unlisted' && !release.slug) return res.status(404).json({ error: 'Changelog not found' });

    const viewer = await optionalAuth(req);
    if (release.status === 'draft' && (!viewer || viewer.uid !== release.authorId)) {
      return res.status(404).json({ error: 'Changelog not found' });
    }
    if (release.status === 'published') {
      snap.ref.update({ views: admin.firestore.FieldValue.increment(1) }).catch(() => {});
    }

    let organization = null;
    if (release.orgSlug) {
      const osnap = await db.collection(ORGANIZATIONS).doc(release.orgSlug).get();
      if (osnap.exists) organization = hub.serializeOrganization({ ...osnap.data(), slug: release.orgSlug });
    }

    let viewerRole = null;
    let isFollowing = false;
    if (viewer) {
      if (release.orgSlug) {
        const membership = await getMembership(release.orgSlug, viewer.uid);
        viewerRole = membership ? (hub.normalizeRole(membership.role) || hub.DEFAULT_ORG_ROLE) : null;
      }
      const targetType = release.orgSlug ? 'organization' : 'user';
      const targetId = release.orgSlug || release.authorId;
      if (targetId) {
        const f = await db.collection(FOLLOWS).doc(hub.followDocId(viewer.uid, targetType, targetId)).get();
        isFollowing = f.exists;
      }
    }

    res.json({ release, organization, viewerRole, isFollowing });
  } catch (err) {
    console.error('Changelog detail error:', err.message);
    res.status(500).json({ error: 'Server error' });
  }
});

app.patch('/api/changelogs/:id', async (req, res) => {
  const decoded = await authenticate(req, res);
  if (!decoded) return;
  const id = String(req.params.id || '');
  if (!/^[a-zA-Z0-9_-]{1,80}$/.test(id)) return res.status(400).json({ error: 'Invalid changelog id' });

  const input = (req.body && typeof req.body === 'object') ? req.body : {};
  const has = key => Object.prototype.hasOwnProperty.call(input, key);
  const errors = [];
  const update = {};
  if (has('title')) {
    const title = hub.cleanString(input.title, 140);
    if (!title) errors.push('title cannot be empty'); else update.title = title;
  }
  if (has('changelog')) {
    const body = typeof input.changelog === 'string' ? input.changelog.trim() : '';
    if (!body) errors.push('changelog cannot be empty');
    else if (body.length > 50000) errors.push('changelog must be under 50000 characters');
    else update.changelog = body;
  }
  if (has('version')) update.version = hub.cleanString(input.version, 40);
  if (has('tags')) update.tags = hub.parseTags(input.tags);
  if (has('category')) update.category = hub.normalizeCategory(input.category);
  if (has('visibility')) update.visibility = input.visibility === 'unlisted' ? 'unlisted' : 'public';
  if (!Object.keys(update).length && !errors.length) errors.push('nothing to update');
  if (errors.length) return res.status(400).json({ error: 'Invalid changelog', details: errors });

  try {
    const ref = db.collection(RELEASES).doc(id);
    const snap = await ref.get();
    if (!snap.exists) return res.status(404).json({ error: 'Changelog not found' });
    const data = snap.data() || {};
    let allowed = data.uid === decoded.uid;
    if (!allowed && data.orgSlug) {
      const membership = await getMembership(data.orgSlug, decoded.uid);
      allowed = !!(membership && hub.canCreateChangelog(hub.normalizeRole(membership.role)));
    }
    if (!allowed) return res.status(403).json({ error: 'Forbidden', message: 'You cannot edit this changelog.' });

    update.updatedAt = admin.firestore.FieldValue.serverTimestamp();
    await ref.set(update, { merge: true });
    res.json({ success: true, id });
  } catch (err) {
    console.error('Edit changelog error:', err.message);
    res.status(500).json({ error: 'Server error' });
  }
});

app.delete('/api/changelogs/:id', async (req, res) => {
  const decoded = await authenticate(req, res);
  if (!decoded) return;
  const id = String(req.params.id || '');
  if (!/^[a-zA-Z0-9_-]{1,80}$/.test(id)) return res.status(400).json({ error: 'Invalid changelog id' });
  try {
    const ref = db.collection(RELEASES).doc(id);
    const snap = await ref.get();
    if (!snap.exists) return res.status(404).json({ error: 'Changelog not found' });
    const data = snap.data() || {};
    let allowed = data.uid === decoded.uid;
    if (!allowed && data.orgSlug) {
      const membership = await getMembership(data.orgSlug, decoded.uid);
      allowed = !!(membership && hub.canManageChangelogs(hub.normalizeRole(membership.role)));
    }
    if (!allowed) return res.status(403).json({ error: 'Forbidden', message: 'You cannot delete this changelog.' });

    await ref.delete();
    if (data.orgSlug && data.status === 'published') {
      await db.collection(ORGANIZATIONS).doc(data.orgSlug).set({ changelogCount: admin.firestore.FieldValue.increment(-1) }, { merge: true }).catch(() => {});
    }
    if (data.projectSlug && data.status === 'published') {
      await db.collection(PROJECTS).doc(data.projectSlug).set({ releaseCount: admin.firestore.FieldValue.increment(-1) }, { merge: true }).catch(() => {});
    }
    res.json({ success: true });
  } catch (err) {
    console.error('Delete changelog error:', err.message);
    res.status(500).json({ error: 'Server error' });
  }
});

app.post('/api/changelogs/:id/publish', async (req, res) => {
  const decoded = await authenticate(req, res);
  if (!decoded) return;
  const id = String(req.params.id || '');
  if (!/^[a-zA-Z0-9_-]{1,80}$/.test(id)) return res.status(400).json({ error: 'Invalid changelog id' });
  try {
    const ref = db.collection(RELEASES).doc(id);
    const snap = await ref.get();
    if (!snap.exists) return res.status(404).json({ error: 'Changelog not found' });
    const data = snap.data() || {};
    let allowed = data.uid === decoded.uid;
    if (!allowed && data.orgSlug) {
      const membership = await getMembership(data.orgSlug, decoded.uid);
      allowed = !!(membership && hub.canCreateChangelog(hub.normalizeRole(membership.role)));
    }
    if (!allowed) return res.status(403).json({ error: 'Forbidden', message: 'You cannot publish this changelog.' });
    if (data.status === 'published') return res.json({ success: true, status: 'published' });

    const now = admin.firestore.FieldValue.serverTimestamp();
    await ref.set({ status: 'published', publishedAt: data.publishedAt || now, updatedAt: now }, { merge: true });
    if (data.orgSlug) {
      await db.collection(ORGANIZATIONS).doc(data.orgSlug).set({ changelogCount: admin.firestore.FieldValue.increment(1), lastReleaseAt: now, updatedAt: now }, { merge: true });
    }
    if (data.projectSlug) {
      await db.collection(PROJECTS).doc(data.projectSlug).set({ releaseCount: admin.firestore.FieldValue.increment(1), lastReleaseAt: now, updatedAt: now }, { merge: true });
    }
    res.json({ success: true, status: 'published' });
  } catch (err) {
    console.error('Publish changelog error:', err.message);
    res.status(500).json({ error: 'Server error' });
  }
});

// ════════════════════════════════
// ── FOLLOWS & PERSONALIZED FEED ──
// ════════════════════════════════

app.get('/api/feed/me', async (req, res) => {
  const decoded = await authenticate(req, res);
  if (!decoded) return;
  try {
    const snap = await db.collection(FOLLOWS).where('followerUid', '==', decoded.uid).limit(500).get();
    const userTargets = [];
    const orgTargets = [];
    snap.forEach(doc => {
      const data = doc.data();
      if (data.targetType === 'user') userTargets.push(data.targetId);
      else if (data.targetType === 'organization') orgTargets.push(data.targetId);
    });

    if (!userTargets.length && !orgTargets.length) {
      return res.json({ items: [], nextCursor: null, followedUsers: 0, followedOrganizations: 0 });
    }

    const wantedUsers = new Set(userTargets);
    const wantedOrgs = new Set(orgTargets);
    const all = await loadPublicChangelogs(1000);
    const items = [];
    for (const c of all) {
      if (c.authorType === 'organization') {
        if (c.orgSlug && wantedOrgs.has(c.orgSlug)) items.push(c);
      } else if (c.authorId && wantedUsers.has(c.authorId)) {
        items.push(c);
      }
    }
    const page = hub.paginate(hub.sortByDateDesc(items, 'publishedAt'), { cursor: req.query.cursor, limit: req.query.limit });
    res.json({ items: page.items, nextCursor: page.nextCursor, total: page.total, followedUsers: userTargets.length, followedOrganizations: orgTargets.length });
  } catch (err) {
    console.error('Personal feed error:', err.message);
    res.status(500).json({ error: 'Server error' });
  }
});

app.get('/api/me/following', async (req, res) => {
  const decoded = await authenticate(req, res);
  if (!decoded) return;
  try {
    const snap = await db.collection(FOLLOWS).where('followerUid', '==', decoded.uid).limit(500).get();
    const users = [];
    const organizations = [];
    for (const doc of snap.docs) {
      const data = doc.data();
      if (data.targetType === 'user') users.push(await publicUserSummary(data.targetId));
      else if (data.targetType === 'organization') {
        const osnap = await db.collection(ORGANIZATIONS).doc(data.targetId).get();
        if (osnap.exists) organizations.push(hub.serializeOrganization({ ...osnap.data(), slug: data.targetId }));
      }
    }
    res.json({ users, organizations });
  } catch (err) {
    console.error('Following list error:', err.message);
    res.status(500).json({ error: 'Server error' });
  }
});

app.get('/api/users/:uid/followers', async (req, res) => {
  try {
    const snap = await db.collection(FOLLOWS).where('targetType', '==', 'user').where('targetId', '==', req.params.uid).limit(200).get();
    const items = [];
    for (const doc of snap.docs) items.push(await publicUserSummary(doc.data().followerUid));
    res.json({ items });
  } catch (err) {
    console.error('User followers error:', err.message);
    res.status(500).json({ error: 'Server error' });
  }
});

app.get('/api/users/:uid/following', async (req, res) => {
  try {
    const snap = await db.collection(FOLLOWS).where('followerUid', '==', req.params.uid).limit(200).get();
    const users = [];
    const organizations = [];
    for (const doc of snap.docs) {
      const data = doc.data();
      if (data.targetType === 'user') users.push(await publicUserSummary(data.targetId));
      else if (data.targetType === 'organization') {
        const osnap = await db.collection(ORGANIZATIONS).doc(data.targetId).get();
        if (osnap.exists) organizations.push(hub.serializeOrganization({ ...osnap.data(), slug: data.targetId }));
      }
    }
    res.json({ users, organizations });
  } catch (err) {
    console.error('User following error:', err.message);
    res.status(500).json({ error: 'Server error' });
  }
});

app.get('/api/follow/check', async (req, res) => {
  const viewer = await optionalAuth(req);
  const targetType = String(req.query.targetType || '');
  const targetId = String(req.query.targetId || '');
  if (!viewer || !hub.isValidFollowTarget(targetType, targetId)) return res.json({ following: false });
  try {
    const snap = await db.collection(FOLLOWS).doc(hub.followDocId(viewer.uid, targetType, targetId)).get();
    res.json({ following: snap.exists });
  } catch (err) {
    res.json({ following: false });
  }
});

// ═══════════════════════════════
// ── DISCOVERY & SEARCH ──
// ═══════════════════════════════

app.get('/api/discover', async (req, res) => {
  try {
    const published = await loadPublicChangelogs(1000);
    const organizations = await loadOrganizations(500);

    const latest = hub.sortByDateDesc(published, 'publishedAt').slice(0, 12);
    const trending = hub.rankByTrending(published, { now: Date.now() }).slice(0, 12);

    // Creators aggregated from real published changelogs only — no invented users.
    const byAuthor = new Map();
    for (const c of published) {
      if (c.authorType === 'organization' || !c.authorId) continue;
      const entry = byAuthor.get(c.authorId) || {
        uid: c.authorId, username: null, displayName: c.author,
        changelogCount: 0, views: 0, stars: 0, lastPublishedAt: null,
      };
      entry.changelogCount += 1;
      entry.views += c.views || 0;
      entry.stars += c.stars || 0;
      if (!entry.lastPublishedAt || hub.toMillis(c.publishedAt) > hub.toMillis(entry.lastPublishedAt)) entry.lastPublishedAt = c.publishedAt;
      if (c.author) entry.displayName = c.author;
      if (c.authorUsername) entry.username = c.authorUsername;
      byAuthor.set(c.authorId, entry);
    }
    const creators = [...byAuthor.values()]
      .sort((a, b) => b.changelogCount - a.changelogCount || hub.toMillis(b.lastPublishedAt) - hub.toMillis(a.lastPublishedAt))
      .slice(0, 8);

    const popularOrganizations = [...organizations]
      .sort((a, b) => (b.followerCount || 0) - (a.followerCount || 0) || (b.changelogCount || 0) - (a.changelogCount || 0) || hub.toMillis(b.createdAt) - hub.toMillis(a.createdAt))
      .slice(0, 8);

    const counts = {};
    for (const c of published) counts[c.category] = (counts[c.category] || 0) + 1;
    const categories = hub.CATEGORIES.map(c => ({ id: c.id, label: c.label, changelogCount: counts[c.id] || 0 }));

    res.json({
      latest,
      trending,
      creators,
      organizations: popularOrganizations,
      categories,
      totals: { changelogs: published.length, organizations: organizations.length, creators: byAuthor.size },
    });
  } catch (err) {
    console.error('Discover error:', err.message);
    res.status(500).json({ error: 'Server error' });
  }
});

app.get('/api/search', async (req, res) => {
  const { q, type, category, tag, from, to, author, org, cursor, limit } = req.query;
  const query = hub.cleanString(q, 120);
  const scope = ['all', 'changelogs', 'users', 'organizations'].includes(type) ? type : 'all';
  try {
    const result = { query, scope, changelogs: { items: [], nextCursor: null, total: 0 }, users: [], organizations: [] };

    if (scope === 'all' || scope === 'changelogs') {
      const all = await loadPublicChangelogs(1000);
      const filtered = hub.filterChangelogs(all, {
        query,
        category: category || 'all',
        tag: tag || '',
        orgSlug: org ? hub.slugify(org) : '',
        authorUsername: author ? hub.normalizeUsername(author) : '',
        from: from || '',
        to: to || '',
      });
      result.changelogs = hub.paginate(filtered, { cursor, limit });
    }

    if (scope === 'all' || scope === 'users') {
      const users = await loadUsers(1000);
      result.users = users.filter(u => hub.matchesQuery(u, query, ['username', 'displayName', 'bio'])).slice(0, 20);
    }

    if (scope === 'all' || scope === 'organizations') {
      const orgs = await loadOrganizations(500);
      result.organizations = orgs
        .filter(o => hub.matchesQuery(o, query, ['name', 'description', 'tags']))
        .slice(0, 20);
    }

    res.json(result);
  } catch (err) {
    console.error('Search error:', err.message);
    res.status(500).json({ error: 'Server error' });
  }
});

// ═══════════════════════════════════════
// ── SEO: META-INJECTED PUBLIC PAGES ──
// ═══════════════════════════════════════
// The page templates stay client-rendered, but the <head> is filled in on the
// server so crawlers and social previews see a real title/description/OG tag.

function renderMetaPage(templateFile, meta) {
  try {
    const file = path.join(__dirname, 'public', templateFile);
    let html = fs.readFileSync(file, 'utf8');
    if (html.includes('<!--FORKD_META-->')) {
      html = html.replace('<!--FORKD_META-->', hub.renderMetaTags(meta, { siteName: 'Forkd' }));
    }
    return html;
  } catch (err) {
    console.error('Meta page render error:', err.message);
    return null;
  }
}

app.get('/p/:slug', async (req, res) => {
  const slug = hub.slugify(req.params.slug);
  let meta = {
    title: 'Project — Forkd',
    description: 'This project page is not available.',
    robots: 'noindex,follow',
  };

  if (hub.isValidSlug(slug)) {
    try {
      const snap = await db.collection(PROJECTS).doc(slug).get();
      if (snap.exists) {
        const project = serializeProjectDoc(snap);
        meta = {
          title: `${project.name} changelogs · Forkd`,
          description: hub.excerpt(project.description, 200) || `Release notes and changelogs for ${project.name}.`,
          url: `${SITE_URL}/p/${slug}`,
          image: project.logoUrl || `${SITE_URL}/logo.png`,
          type: 'website',
        };
      }
    } catch (err) {
      console.error('Project meta error:', err.message);
    }
  }

  const html = renderMetaPage('project.html', meta);
  if (!html) return res.status(404).send('Not found');
  res.type('html').send(html);
});

app.get('/shared/:id', async (req, res) => {
  const id = String(req.params.id || '').replace(/[^a-zA-Z0-9_-]/g, '');
  let meta = {
    title: 'Changelog — Forkd',
    description: 'This changelog is not available.',
    robots: 'noindex,follow',
  };

  if (id) {
    try {
      const snap = await db.collection(RELEASES).doc(id).get();
      if (snap.exists && serializeReleaseDoc(snap).status !== 'draft') {
        const release = serializeReleaseDoc(snap);
        const headline = release.projectName
          ? `${release.projectName} ${release.version || ''}`.trim()
          : release.title;
        meta = {
          title: `${release.title}${release.version ? ` ${release.version}` : ''} · ${release.projectName || 'Forkd'}`,
          description: release.excerpt || `A changelog published by ${release.author} on Forkd.`,
          url: `${SITE_URL}/shared/${id}`,
          image: `${SITE_URL}/logo.png`,
          type: 'article',
          jsonLd: {
            '@context': 'https://schema.org',
            '@type': 'TechArticle',
            headline,
            datePublished: release.createdAt || undefined,
            author: { '@type': 'Person', name: release.author },
            publisher: { '@type': 'Organization', name: 'Forkd' },
          },
        };
      }
    } catch (err) {
      console.error('Release meta error:', err.message);
    }
  }

  const html = renderMetaPage('shared.html', meta);
  if (!html) return res.status(404).send('Not found');
  res.type('html').send(html);
});

// ── PUBLIC CHANGELOG PAGE (/changelog/:slug) ──
app.get('/changelog/:slug', async (req, res) => {
  const key = String(req.params.slug || '').trim().toLowerCase();
  let meta = {
    title: 'Changelog — Forkd',
    description: 'This changelog is not available.',
    robots: 'noindex,follow',
  };

  if (/^[a-z0-9_-]{1,80}$/.test(key)) {
    try {
      let snap = await db.collection(RELEASES).doc(key).get();
      if (!snap.exists) {
        const bySlug = await db.collection(RELEASES).where('slug', '==', key).limit(1).get();
        if (bySlug.docs.length) snap = bySlug.docs[0];
      }
      if (snap.exists && serializeReleaseDoc(snap).status !== 'draft') {
        const release = serializeReleaseDoc(snap);
        const headline = release.orgName
          ? `${release.orgName} ${release.version || ''}`.trim()
          : release.title;
        meta = {
          title: `${release.title}${release.version ? ` ${release.version}` : ''} · ${release.orgName || release.author}`,
          description: release.excerpt || `A changelog published by ${release.author} on Forkd.`,
          url: `${SITE_URL}/changelog/${release.slug || snap.id}`,
          image: `${SITE_URL}/logo.png`,
          type: 'article',
          jsonLd: {
            '@context': 'https://schema.org',
            '@type': 'TechArticle',
            headline,
            datePublished: release.publishedAt || release.createdAt || undefined,
            author: release.authorType === 'organization'
              ? { '@type': 'Organization', name: release.orgName }
              : { '@type': 'Person', name: release.author },
            publisher: { '@type': 'Organization', name: 'Forkd' },
          },
        };
      }
    } catch (err) {
      console.error('Changelog meta error:', err.message);
    }
  }

  const html = renderMetaPage('changelog.html', meta);
  if (!html) return res.status(404).send('Not found');
  res.type('html').send(html);
});

// ── PUBLIC ORGANIZATION PAGE (/org/:slug) ──
app.get('/org/:slug', async (req, res) => {
  const slug = hub.slugify(req.params.slug);
  let meta = {
    title: 'Organization — Forkd',
    description: 'This organization page is not available.',
    robots: 'noindex,follow',
  };

  if (hub.isValidSlug(slug)) {
    try {
      const snap = await db.collection(ORGANIZATIONS).doc(slug).get();
      if (snap.exists) {
        const organization = hub.serializeOrganization({ ...snap.data(), slug });
        meta = {
          title: `${organization.name} changelogs · Forkd`,
          description: hub.excerpt(organization.description, 200) || `Software updates and changelogs from ${organization.name}.`,
          url: `${SITE_URL}/org/${slug}`,
          image: organization.avatarUrl || `${SITE_URL}/logo.png`,
          type: 'website',
        };
      }
    } catch (err) {
      console.error('Organization meta error:', err.message);
    }
  }

  const html = renderMetaPage('org.html', meta);
  if (!html) return res.status(404).send('Not found');
  res.type('html').send(html);
});

// ── PUBLIC PROFILE PAGE (/@username) ──
app.get('/@:username', async (req, res) => {
  const username = hub.normalizeUsername(req.params.username);
  let meta = {
    title: 'Profile — Forkd',
    description: 'This profile is not available.',
    robots: 'noindex,follow',
  };

  if (username) {
    try {
      const user = await findUserByUsername(username);
      if (user) {
        const profile = hub.serializeUserProfile({ ...user, uid: user.uid });
        meta = {
          title: `${profile.displayName} (@${username}) · Forkd`,
          description: profile.bio || `Software changelogs published by ${profile.displayName} on Forkd.`,
          url: `${SITE_URL}/@${username}`,
          image: profile.photoURL || `${SITE_URL}/logo.png`,
          type: 'profile',
        };
      }
    } catch (err) {
      console.error('Profile meta error:', err.message);
    }
  }

  const html = renderMetaPage('user-profile.html', meta);
  if (!html) return res.status(404).send('Not found');
  res.type('html').send(html);
});

// ── SITEMAP + ROBOTS ──
app.get('/sitemap.xml', async (req, res) => {
  try {
    const projects = await loadProjects(1000);
    const releases = await loadReleases(2000);
    const organizations = await loadOrganizations(1000);
    const users = await loadUsers(1000);

    const urls = [
      { loc: `${SITE_URL}/`, changefreq: 'daily', priority: '1.0' },
      { loc: `${SITE_URL}/discover`, changefreq: 'hourly', priority: '0.9' },
    ];
    for (const project of projects) {
      if (project.moderationStatus === 'hidden') continue;
      urls.push({
        loc: `${SITE_URL}/p/${project.slug}`,
        lastmod: project.lastReleaseAt || project.createdAt,
        changefreq: 'weekly',
        priority: '0.8',
      });
    }
    for (const organization of organizations) {
      urls.push({
        loc: `${SITE_URL}/org/${organization.slug}`,
        lastmod: organization.lastReleaseAt || organization.createdAt,
        changefreq: 'weekly',
        priority: '0.8',
      });
    }
    for (const user of users) {
      if (!user.username) continue;
      urls.push({ loc: `${SITE_URL}/@${user.username}`, changefreq: 'weekly', priority: '0.6' });
    }
    for (const release of releases) {
      if (release.status !== 'published') continue;
      const loc = release.slug ? `${SITE_URL}/changelog/${release.slug}` : `${SITE_URL}/shared/${release.id}`;
      urls.push({ loc, lastmod: release.publishedAt || release.createdAt, changefreq: 'monthly', priority: '0.6' });
    }

    const xml = `<?xml version="1.0" encoding="UTF-8"?>\n<urlset xmlns="http://www.sitemaps.org/schemas/sitemap/0.9">\n${urls
      .map(u => {
        const lastmod = u.lastmod ? `<lastmod>${new Date(u.lastmod).toISOString().slice(0, 10)}</lastmod>` : '';
        return `  <url><loc>${hub.escapeXml(u.loc)}</loc>${lastmod}<changefreq>${u.changefreq}</changefreq><priority>${u.priority}</priority></url>`;
      })
      .join('\n')}\n</urlset>`;

    res.type('application/xml').send(xml);
  } catch (err) {
    console.error('Sitemap error:', err.message);
    res.status(500).type('text/plain').send('Server error');
  }
});

app.get('/robots.txt', (req, res) => {
  res.type('text/plain').send(
    [
      'User-agent: *',
      'Allow: /',
      'Disallow: /dashboard.html',
      'Disallow: /changelog_generator.html',
      'Disallow: /login.html',
      'Disallow: /vscode-auth.html',
      'Disallow: /api/',
      '',
      `Sitemap: ${SITE_URL}/sitemap.xml`,
      '',
    ].join('\n')
  );
});

// ── PUBLIC PAGES ──
app.get('/discover', (req, res) => {
  res.sendFile('public/discover.html', { root: __dirname });
});

app.get('/publish', (req, res) => {
  res.sendFile('public/publish.html', { root: __dirname });
});

app.get('/u/:uid', (req, res) => {
  res.sendFile('public/user-profile.html', { root: __dirname });
});

app.listen(PORT, () => console.log(`Server live on port ${PORT}`));
