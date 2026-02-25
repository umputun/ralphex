# Project Type Detection

Detect the primary language/framework by checking for marker files in the project root.

## Detection Rules

| Marker File(s) | Language | Base Image | Custom Dockerfile? |
|----------------|----------|------------|--------------------|
| `go.mod` | Go | `ghcr.io/umputun/ralphex-go:latest` | No |
| `package.json` | Node.js | `ghcr.io/umputun/ralphex:latest` | No (node pre-installed) |
| `requirements.txt`, `pyproject.toml`, `setup.py`, `Pipfile` | Python | `ghcr.io/umputun/ralphex:latest` | No (python pre-installed) |
| `Cargo.toml` | Rust | `ghcr.io/umputun/ralphex:latest` | Yes |
| `pom.xml`, `build.gradle`, `build.gradle.kts` | Java/Kotlin | `ghcr.io/umputun/ralphex:latest` | Yes |
| `*.csproj`, `*.sln`, `*.fsproj` | C#/.NET | `ghcr.io/umputun/ralphex:latest` | Yes |
| `Gemfile` | Ruby | `ghcr.io/umputun/ralphex:latest` | Yes |
| `mix.exs` | Elixir | `ghcr.io/umputun/ralphex:latest` | Yes |
| none of the above | Generic | `ghcr.io/umputun/ralphex:latest` | No |

## Detection Commands

Run these checks using Glob or Bash:

```bash
# check for marker files in project root
ls go.mod package.json requirements.txt pyproject.toml setup.py Pipfile \
   Cargo.toml pom.xml build.gradle build.gradle.kts mix.exs Gemfile 2>/dev/null
```

For C#/.NET, use Glob:
```
*.csproj
*.sln
*.fsproj
```

## Multi-Language Projects

If multiple markers are found (e.g., `go.mod` + `package.json`):

1. Present detected languages to user via AskUserQuestion
2. Ask which is the **primary language** for ralphex execution
3. Use the primary language's image/Dockerfile rules

Example question:
- header: "Language"
- question: "Detected both Go and Node.js. Which is the primary language for ralphex?"
- options: detected languages + "Other"

## Version Detection Hints

For languages requiring custom Dockerfiles, try to detect the version:

| Language | Version Source | Fallback |
|----------|---------------|----------|
| Rust | `rust-toolchain.toml` or `rust-toolchain` file | `latest` |
| Java | `pom.xml` (`<java.version>` or `<maven.compiler.source>`), `build.gradle` (`sourceCompatibility`) | `21` |
| C#/.NET | `*.csproj` (`<TargetFramework>net8.0</TargetFramework>`) | `8.0` |
| Ruby | `.ruby-version` file or `Gemfile` (`ruby '3.x'`) | `3.3` |
| Elixir | `.tool-versions` or `mix.exs` (`elixir: "~> 1.x"`) | `1.17` |

Use detected version in the generated Dockerfile when possible.
