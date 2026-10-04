# WSPortal workspace file
# This file describes a portable working environment.
# It is safe to read, edit, version-control, and share.
# Do not store secrets (passwords, tokens, keys) in this file.
version: 2
workspace:
  name: myproject
  description: MyProject development environment
project:
  name: myproject
  source:
    type: git
    url: https://github.com/user/myproject.git
    branch: main
  path: ${WORKSPACE_ROOT}/myproject
applications:
  - id: editor
    name: vscode
    open:
      - ${WORKSPACE_ROOT}/myproject
  - id: terminal
    name: terminal
    working_directory: ${WORKSPACE_ROOT}/myproject
browser:
  - browser: chrome
    windows:
      - tabs:
          - url: https://github.com/user/myproject
          - url: http://localhost:3000
          - url: https://docs.example.com
terminals:
  - name: server
    working_directory: ${WORKSPACE_ROOT}/myproject
    command: npm run dev
services:
  - name: docker
    required: true
  - name: postgres
    version: "16"
    required: true
environment:
  runtime:
    node: "22"
  tools:
    - git
    - docker
metadata:
  created_by: wsportal
