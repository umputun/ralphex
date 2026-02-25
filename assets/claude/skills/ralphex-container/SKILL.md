---
description: >-
  This skill should be used when the user asks to "set up ralphex docker",
  "configure ralphex container", "run ralphex in docker", "dockerize for ralphex",
  "ralphex container setup", or wants to set up Docker-based ralphex execution
  for their project directory.
allowed-tools: [Bash, Read, Write, Glob, Grep, AskUserQuestion]
---

# ralphex-container - Docker Setup for ralphex

**SCOPE**: Detect the project language, select the appropriate Docker image, and generate configuration files for running ralphex in a container.

## Step 0: Prerequisites

Check if ralphex CLI is installed (needed to run ralphex in the container after setup):
```bash
which ralphex
```

**If not found**, inform user they'll need it to run ralphex in the container:
- **macOS (Homebrew)**: `brew install umputun/apps/ralphex`
- **Linux (Debian/Ubuntu)**: download `.deb` from https://github.com/umputun/ralphex/releases
- **Linux (RHEL/Fedora)**: download `.rpm` from https://github.com/umputun/ralphex/releases
- **Any platform with Go**: `go install github.com/umputun/ralphex/cmd/ralphex@latest`

Proceed with container setup regardless, but remind user to install before execution.

Verify Docker is installed:

```bash
docker --version
```

**If not found**, stop and tell the user:
- Docker is required for container-based ralphex execution
- Install from https://docs.docker.com/get-docker/

**Do not proceed until `docker --version` succeeds.**

## Step 0.5: Choose File Location

Use AskUserQuestion to determine where Docker files should be generated:
- header: "File location"
- question: "Where should ralphex Docker files be generated?"
- options:
  - label: ".ralphex/ directory (Recommended)"
    description: "Keeps Docker config grouped with other ralphex settings"
  - label: "Project root"
    description: "Place Dockerfile.ralphex and docker-compose.ralphex.yml in the project root"

If `.ralphex/` is chosen, create the directory:

```bash
mkdir -p .ralphex
```

**Remember the chosen location for Steps 1, 5, 6, 7, and 8.**

## Step 1: Check Existing Configuration

Look for existing ralphex Docker files in the chosen location:

```bash
# if .ralphex/ was chosen:
ls .ralphex/Dockerfile.ralphex .ralphex/docker-compose.ralphex.yml 2>/dev/null

# if project root was chosen:
ls Dockerfile.ralphex docker-compose.ralphex.yml 2>/dev/null
```

**If files exist**, use AskUserQuestion:
- header: "Overwrite"
- question: "Found existing ralphex Docker config. Overwrite?"
- options:
  - label: "Yes, regenerate"
    description: "Replace existing files with fresh configuration"
  - label: "No, abort"
    description: "Keep existing files unchanged"

If user chooses "No, abort", stop and report existing setup.

## Step 2: Detect Project Type

Check for language marker files in the project root. See `references/project-detection.md` for the full detection table.

Run detection:

```bash
ls go.mod package.json requirements.txt pyproject.toml setup.py Pipfile \
   Cargo.toml pom.xml build.gradle build.gradle.kts mix.exs Gemfile 2>/dev/null
```

Also check for C#/.NET using Glob patterns: `*.csproj`, `*.sln`, `*.fsproj`

**If multiple languages detected**, ask user to pick primary (see `references/project-detection.md` multi-language section).

**If no markers found**, default to generic setup with `ghcr.io/umputun/ralphex:latest`.

Confirm detection with user via AskUserQuestion:
- header: "Language"
- question: "Detected [language]. Use [image] as base?"
- options:
  - label: "Yes" with description of the detected setup
  - label: "Different language" with description to specify manually

## Step 3: Analyze Project Dependencies

Scan the project for dependencies that need extra system packages in the Docker image. See `references/dependency-analysis.md` for the full detection rules.

Run the language-appropriate grep checks from the reference file based on the language detected in Step 2. Also run the Docker/Testcontainers detection checks regardless of language. Collect:
- **Extra packages**: `apk add` packages needed (e.g., `sqlite-dev`, `vips-dev`, `docker-cli`)
- **Extra RUN lines**: additional Dockerfile commands (e.g., `npm install -g yarn`)
- **Extra volumes**: additional Docker volume mounts (e.g., `/var/run/docker.sock:/var/run/docker.sock`)
- **Extra environment**: additional environment variables (e.g., `DOCKER_HOST`, `TESTCONTAINERS_RYUK_DISABLED`)
- **Needs Docker socket**: `true` if testcontainers or Docker SDK detected

If extra dependencies are found, inform the user:
- "Detected [dependency] requiring [packages]"
- Note that a custom Dockerfile will be generated (even for Go/Python/Node.js)

If Docker usage (testcontainers or Docker SDK) is detected, inform the user:
- "Detected [testcontainers-go / Docker SDK / etc.] — Docker socket will be mounted into the container"
- "This allows tests to spawn Docker containers via the host daemon"
- "**Security note**: mounting the Docker socket grants the container full access to the host Docker daemon"

If only project Docker files are found (Dockerfile, docker-compose.yml) without testcontainers or Docker SDK usage, ask the user via AskUserQuestion whether they need Docker access for tests inside the container.

If no extra dependencies are found, report "No extra system packages needed" and move on.

## Step 4: Choose Setup Approach

Use AskUserQuestion to determine what to generate:
- header: "Setup"
- question: "How do you want to run ralphex in Docker?"
- options:
  - label: "Wrapper script (Recommended)"
    description: "Install ralphex-dk.sh — handles credentials, symlinks, worktrees automatically"
  - label: "docker-compose"
    description: "Generate docker-compose.ralphex.yml for docker compose workflow"
  - label: "Both"
    description: "Install wrapper script and generate docker-compose file"

## Step 4.5: macOS Credential Setup

**Only if user chose "docker-compose" or "Both" in Step 4.**

On macOS, Claude Code stores credentials in the macOS Keychain rather than as a file on disk. The docker-compose setup mounts `~/.claude:/mnt/claude:ro`, but if `~/.claude/.credentials.json` doesn't exist, the container starts without credentials.

Detect the platform:

```bash
uname -s
```

**If output is `Darwin` (macOS):**

1. Check if credentials file already exists:
```bash
ls ~/.claude/.credentials.json 2>/dev/null
```

2. **If the file does NOT exist**, extract credentials from Keychain and save to disk:
```bash
security find-generic-password -s "Claude Code-credentials" -w > ~/.claude/.credentials.json && chmod 600 ~/.claude/.credentials.json
```

3. If the `security` command fails (keychain locked or no credentials found), inform the user:
   - "Could not extract Claude credentials from macOS Keychain. Make sure you're logged into Claude Code (`claude` in terminal) and try again."
   - Do not proceed with docker-compose setup until credentials are available.

4. If credentials were successfully saved, inform the user:
   - "Saved Claude credentials to `~/.claude/.credentials.json` for Docker access."
   - "Note: the wrapper script (`ralphex-dk.sh`) uses temporary files for credentials instead, which is more secure. Consider using the wrapper if credential security is a concern."

**If not macOS**, skip this step — credentials are already stored on disk.

## Step 5: Generate Dockerfile (if needed)

**Skip this step** if ALL of these are true:
- The project uses a pre-built image language (Go, Python, or Node.js)
- Step 3 found no extra dependencies

**Generate `Dockerfile.ralphex`** if EITHER:
- The project language requires a custom Dockerfile (Rust, Java, C#/.NET, Ruby, Elixir) — use templates from `references/dockerfile-templates.md`
- Step 3 found extra dependencies for a pre-built image language — use the "Extending Pre-Built Images" templates from `references/dockerfile-templates.md`

For pre-built image extensions, use the correct base:
- Go projects: `FROM ghcr.io/umputun/ralphex-go:latest`
- Python/Node.js projects: `FROM ghcr.io/umputun/ralphex:latest`

Append any extra `apk add` packages and extra `RUN` lines from Step 3 to the Dockerfile.

Try to detect the language version (see `references/project-detection.md` version detection hints) and use it in the Dockerfile.

Write the file to the chosen location:
```
# if .ralphex/ was chosen:
.ralphex/Dockerfile.ralphex

# if project root was chosen:
Dockerfile.ralphex
```

## Step 6: Generate docker-compose.ralphex.yml (if selected)

**Only if user chose "docker-compose" or "Both" in Step 4.**

Use the template from `references/compose-template.md`:
- If a `Dockerfile.ralphex` was generated in Step 5, use the "custom Dockerfile" template variant
- Otherwise, use the "pre-built image" template variant with the correct image name

Replace the `{{IMAGE}}` placeholder with the actual image (e.g., `ghcr.io/umputun/ralphex-go:latest`).

**If `.ralphex/` location was chosen**, use the `.ralphex/` template variants from `references/compose-template.md` instead of the project-root variants. These use `context: ..` for build and `..:/workspace` for volume mounts because Docker Compose resolves paths relative to the compose file's directory.

If Step 3 detected Docker usage (testcontainers or Docker SDK — i.e., **Needs Docker socket** is `true`), append the Docker socket configuration from `references/compose-template.md` "Docker Socket Access" section:
- Add `/var/run/docker.sock:/var/run/docker.sock` to the `volumes:` list
- Add `DOCKER_HOST`, `TESTCONTAINERS_RYUK_DISABLED`, and `TESTCONTAINERS_DOCKER_SOCKET_OVERRIDE` to the `environment:` list (include testcontainers variables only if testcontainers was detected, not for Docker SDK-only)

Write the file to the chosen location:
```
# if .ralphex/ was chosen:
.ralphex/docker-compose.ralphex.yml

# if project root was chosen:
docker-compose.ralphex.yml
```

## Step 7: Install Wrapper Script (if selected)

**Only if user chose "Wrapper script" or "Both" in Step 4.**

Follow the installation guide in `references/wrapper-setup.md`:

1. **Check for existing native `ralphex` binary** before choosing an install path:
```bash
which ralphex 2>/dev/null
```

**If a native binary is found**, warn the user via AskUserQuestion:
   - header: "Conflict"
   - question: "Found native `ralphex` at [path]. The wrapper script would shadow it. How to proceed?"
   - options:
     - label: "Install as ralphex-dk"
       description: "Keep native binary, install wrapper as ralphex-dk to avoid conflicts"
     - label: "Replace native binary"
       description: "Overwrite the native binary path with the wrapper script"
     - label: "Abort wrapper install"
       description: "Skip wrapper script installation entirely"

If user chooses "Install as ralphex-dk", use `ralphex-dk` as the script name in the chosen install path.
If user chooses "Abort", skip the rest of this step.

2. Download the wrapper script:
```bash
curl -sL https://raw.githubusercontent.com/umputun/ralphex/master/scripts/ralphex-dk.sh -o /tmp/ralphex-dk.sh
chmod +x /tmp/ralphex-dk.sh
```

3. Ask user about installation path via AskUserQuestion:
   - header: "Install path"
   - question: "Where to install the wrapper script?"
   - options:
     - label: "~/.local/bin/ (Recommended)"
       description: "User-local, no sudo needed"
     - label: "/usr/local/bin/"
       description: "System-wide — requires running sudo manually"
     - label: "Project-local (./)"
       description: "Only for this project"

4. Install to the chosen location:
   - **~/.local/bin/**: create the directory if needed and move the script directly
   - **/usr/local/bin/**: do NOT run `sudo`. Instead, print the command for the user to run manually:
     ```
     Run this command yourself to complete the install:
       sudo mv /tmp/ralphex-dk.sh /usr/local/bin/ralphex
     ```
   - **Project-local**: move the script to the project root

5. If a custom image is needed (not the default `ralphex-go:latest`), configure `RALPHEX_IMAGE`:
   - Detect user's shell and set accordingly (see `references/wrapper-setup.md` shell configuration)
   - For custom Dockerfiles, remind user to build the image first (path depends on chosen location):
     ```bash
     # if .ralphex/ location:
     docker build -f .ralphex/Dockerfile.ralphex -t ralphex-custom .

     # if project root location:
     docker build -f Dockerfile.ralphex -t ralphex-custom .
     ```

## Step 8: Summary

Report what was generated and provide usage commands. **Use actual paths based on the location chosen in Step 0.5:**

```
ralphex Docker setup complete!

Generated files:
  - [location]/Dockerfile.ralphex (if applicable)
  - [location]/docker-compose.ralphex.yml (if applicable)
  - Wrapper script installed at [path] (if applicable)

Quick start:
  [wrapper commands or docker compose commands based on what was set up]
  [use -f .ralphex/docker-compose.ralphex.yml or -f docker-compose.ralphex.yml accordingly]

Tip: Generated files should be committed to git so other contributors can use them.
```

If Docker socket was detected in Step 3, add a note to the summary:

```
Docker socket access:
  The Docker socket (/var/run/docker.sock) is mounted into the container
  for testcontainers / Docker SDK access. This is configured in docker-compose.ralphex.yml.

  For the wrapper script, set RALPHEX_EXTRA_VOLUMES:
    fish:      set -Ux RALPHEX_EXTRA_VOLUMES /var/run/docker.sock:/var/run/docker.sock
    bash/zsh:  export RALPHEX_EXTRA_VOLUMES="/var/run/docker.sock:/var/run/docker.sock"
```

Adjust the summary based on which options were selected.

## Constraints

- Only generate `.ralphex`-suffixed Docker files to avoid collisions with project's own Docker setup
- Do NOT modify existing `Dockerfile` or `docker-compose.yml` files
- Do NOT create `.env` files — environment configuration belongs in shell profiles
- File location (`.ralphex/` or project root) is user-chosen in Step 0.5
- Do NOT run ralphex execution — this skill only sets up the Docker environment
- Commit generated files to git (they are not secrets)
