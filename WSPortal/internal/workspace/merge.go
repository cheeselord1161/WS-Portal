package workspace

import "github.com/cheeselord1161/WS_Portal/internal/platform"

// Merge combines a freshly captured workspace with an existing one and returns
// the result. It lets `ws capture` refresh a workspace without discarding
// content the user added or curated by hand.
//
// The captured workspace describes what is running right now, so it wins where
// the two overlap. Anything only the existing workspace has — a project source,
// pinned runtime versions, required-service flags, extra applications and tabs —
// is preserved, and list sections are combined without duplicates. Neither
// argument is modified.
func Merge(existing, captured *Workspace) *Workspace {
	switch {
	case existing == nil:
		return captured
	case captured == nil:
		return existing
	}

	out := *captured

	// Identity: the existing name and description may have been curated by the
	// user, so keep them unless the existing workspace is missing them.
	if out.Workspace.Name == "" {
		out.Workspace.Name = existing.Workspace.Name
	}
	if existing.Workspace.Description != "" {
		out.Workspace.Description = existing.Workspace.Description
	}
	if existing.Version > out.Version {
		out.Version = existing.Version
	}

	out.Project = mergeProject(existing.Project, captured.Project)
	out.Applications = mergeApplications(existing.Applications, captured.Applications)
	out.Browser = mergeBrowsers(existing.Browser, captured.Browser)
	out.Terminals = mergeTerminals(existing.Terminals, captured.Terminals)
	out.Services = mergeServices(existing.Services, captured.Services)
	out.Environment = mergeEnvironment(existing.Environment, captured.Environment)

	// Preserve the original creation time; refresh the producer.
	if existing.Metadata.CreatedAt != "" {
		out.Metadata.CreatedAt = existing.Metadata.CreatedAt
	}
	return &out
}

// mergeProject keeps the captured (live) project but fills gaps from the
// existing one, most importantly a source the user configured.
func mergeProject(existing, captured *Project) *Project {
	if existing == nil {
		return captured
	}
	if captured == nil {
		return existing
	}
	out := *captured
	if out.Name == "" {
		out.Name = existing.Name
	}
	if out.Path == "" {
		out.Path = existing.Path
	}
	if out.Source == nil {
		out.Source = existing.Source
	}
	return &out
}

// mergeApplications combines applications by their portable logical identity so
// that the same application captured under a different name or id is not
// duplicated. Existing open paths are kept and newly captured ones appended.
func mergeApplications(existing, captured []Application) []Application {
	out := make([]Application, 0, len(existing)+len(captured))
	index := map[string]int{}
	add := func(app Application) {
		key := platform.CanonicalAppID(app.LogicalName())
		if key == "" {
			key = app.ID
		}
		if key == "" {
			key = app.Name
		}
		i, ok := index[key]
		if !ok {
			index[key] = len(out)
			out = append(out, app)
			return
		}
		merged := out[i]
		merged.Open = mergeStrings(merged.Open, app.Open)
		if merged.Name == "" {
			merged.Name = app.Name
		}
		if merged.ID == "" {
			merged.ID = app.ID
		}
		if merged.WorkingDirectory == "" {
			merged.WorkingDirectory = app.WorkingDirectory
		}
		out[i] = merged
	}
	for _, app := range existing {
		add(app)
	}
	for _, app := range captured {
		add(app)
	}
	return out
}

// mergeBrowsers combines browser sessions by browser and profile, unioning
// their windows and tabs.
func mergeBrowsers(existing, captured []BrowserSession) []BrowserSession {
	out := make([]BrowserSession, 0, len(existing)+len(captured))
	index := map[string]int{}
	add := func(session BrowserSession) {
		session.migrateLegacyTabs()
		key := session.Browser + "\x00" + session.Profile
		i, ok := index[key]
		if !ok {
			index[key] = len(out)
			out = append(out, session)
			return
		}
		merged := out[i]
		merged.Windows = mergeWindows(merged.Windows, session.Windows)
		out[i] = merged
	}
	for _, session := range existing {
		add(session)
	}
	for _, session := range captured {
		add(session)
	}
	return out
}

// mergeWindows combines windows positionally and unions the tabs of each.
func mergeWindows(existing, captured []BrowserWindow) []BrowserWindow {
	if len(existing) == 0 {
		return captured
	}
	if len(captured) == 0 {
		return existing
	}
	n := len(existing)
	if len(captured) > n {
		n = len(captured)
	}
	out := make([]BrowserWindow, 0, n)
	for i := 0; i < n; i++ {
		var tabs []BrowserTab
		if i < len(existing) {
			tabs = append(tabs, existing[i].Tabs...)
		}
		if i < len(captured) {
			tabs = mergeTabs(tabs, captured[i].Tabs)
		}
		out = append(out, BrowserWindow{Tabs: tabs})
	}
	return out
}

// mergeTabs unions tabs by URL. A title from extra fills a missing title in
// base; otherwise the base tab (and its title) is kept.
func mergeTabs(base, extra []BrowserTab) []BrowserTab {
	out := make([]BrowserTab, 0, len(base)+len(extra))
	index := map[string]int{}
	add := func(tab BrowserTab) {
		if tab.URL == "" {
			return
		}
		i, ok := index[tab.URL]
		if !ok {
			index[tab.URL] = len(out)
			out = append(out, tab)
			return
		}
		if out[i].Title == "" {
			out[i].Title = tab.Title
		}
	}
	for _, tab := range base {
		add(tab)
	}
	for _, tab := range extra {
		add(tab)
	}
	return out
}

// mergeTerminals combines terminals, dropping exact duplicates.
func mergeTerminals(existing, captured []Terminal) []Terminal {
	out := make([]Terminal, 0, len(existing)+len(captured))
	seen := map[Terminal]bool{}
	add := func(terminal Terminal) {
		if seen[terminal] {
			return
		}
		seen[terminal] = true
		out = append(out, terminal)
	}
	for _, terminal := range existing {
		add(terminal)
	}
	for _, terminal := range captured {
		add(terminal)
	}
	return out
}

// mergeServices combines services by name. A version or required flag recorded
// in the existing workspace is kept, since capture cannot detect them.
func mergeServices(existing, captured []Service) []Service {
	out := make([]Service, 0, len(existing)+len(captured))
	index := map[string]int{}
	add := func(service Service) {
		i, ok := index[service.Name]
		if !ok {
			index[service.Name] = len(out)
			out = append(out, service)
			return
		}
		merged := out[i]
		if merged.Version == "" {
			merged.Version = service.Version
		}
		merged.Required = merged.Required || service.Required
		out[i] = merged
	}
	for _, service := range existing {
		add(service)
	}
	for _, service := range captured {
		add(service)
	}
	return out
}

// mergeEnvironment unions tools and runtimes. Existing runtime versions win,
// because they were likely pinned deliberately.
func mergeEnvironment(existing, captured Environment) Environment {
	out := captured
	out.Tools = mergeStrings(existing.Tools, captured.Tools)
	out.Runtime = mergeStringMaps(existing.Runtime, captured.Runtime)
	return out
}

// mergeStrings returns the distinct, non-empty values of base followed by extra,
// preserving order.
func mergeStrings(base, extra []string) []string {
	if len(base) == 0 && len(extra) == 0 {
		return nil
	}
	out := make([]string, 0, len(base)+len(extra))
	seen := make(map[string]bool, len(base)+len(extra))
	for _, list := range [][]string{base, extra} {
		for _, value := range list {
			if value == "" || seen[value] {
				continue
			}
			seen[value] = true
			out = append(out, value)
		}
	}
	return out
}

// mergeStringMaps combines two maps, preferring base on conflicts.
func mergeStringMaps(base, extra map[string]string) map[string]string {
	if len(base) == 0 && len(extra) == 0 {
		return nil
	}
	out := make(map[string]string, len(base)+len(extra))
	for key, value := range extra {
		out[key] = value
	}
	for key, value := range base {
		out[key] = value
	}
	return out
}
