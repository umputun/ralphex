# Dependency Analysis

Detect project dependencies that require extra system packages beyond what the base images provide.

**Base image includes:** nodejs, npm, python3, py3-pip, git, bash, fzf, ripgrep, make, gcc, musl-dev, libgcc, libstdc++
**Go image adds:** Go 1.26, golangci-lint, moq, goimports

## Analysis Commands

For each detected language, run the checks below. Collect all extra `apk add` packages and additional `RUN` lines needed.

### Go

Check for CGO and native library usage:

```bash
# CGO usage (requires gcc, already in base)
grep -r 'import "C"' --include='*.go' .

# sqlite3 (needs sqlite-dev)
grep -r 'mattn/go-sqlite3\|modernc.org/sqlite' go.sum

# image processing (needs various libs)
grep -r 'disintegration/imaging\|nfnt/resize' go.sum
```

| Dependency Pattern | Extra Packages |
|-------------------|----------------|
| `import "C"` | none (gcc/musl-dev already in base) |
| `mattn/go-sqlite3` | `sqlite-dev` |
| `modernc.org/sqlite` | none (pure Go) |
| `disintegration/imaging` | `libjpeg-turbo-dev libpng-dev` |

### Node.js

Check `package.json` dependencies for native modules:

```bash
# read dependency names from package.json
grep -E '"(sharp|canvas|bcrypt|sqlite3|better-sqlite3|node-gyp|re2|argon2|cpu-features|bufferutil|utf-8-validate)"' package.json
```

Also detect package manager:

```bash
ls yarn.lock pnpm-lock.yaml .npmrc 2>/dev/null
```

| Dependency | Extra Packages | Extra RUN |
|-----------|----------------|-----------|
| `sharp` | `vips-dev` | — |
| `canvas` | `cairo-dev pango-dev jpeg-dev giflib-dev librsvg-dev` | — |
| `bcrypt` | none (gcc already in base) | — |
| `sqlite3` / `better-sqlite3` | `sqlite-dev python3` | — |
| `argon2` | none (gcc already in base) | — |
| `bufferutil` / `utf-8-validate` | none (gcc already in base) | — |
| yarn detected | — | `RUN npm install -g yarn` |
| pnpm detected | — | `RUN npm install -g pnpm` |

### Python

Check `requirements.txt`, `pyproject.toml`, or `Pipfile` for packages with native extensions:

```bash
# scan all common dependency files
grep -ihE '(numpy|scipy|pillow|lxml|psycopg2|cryptography|cffi|bcrypt|grpcio|pandas|matplotlib|scikit-learn|opencv)' \
  requirements*.txt pyproject.toml Pipfile setup.py setup.cfg 2>/dev/null
```

Also detect package manager:

```bash
ls poetry.lock uv.lock Pipfile.lock 2>/dev/null
```

| Dependency | Extra Packages | Extra RUN |
|-----------|----------------|-----------|
| `numpy` / `scipy` | `openblas-dev gfortran` | — |
| `pillow` | `libjpeg-turbo-dev libpng-dev zlib-dev freetype-dev` | — |
| `lxml` | `libxml2-dev libxslt-dev` | — |
| `psycopg2` (not `-binary`) | `postgresql-dev` | — |
| `cryptography` / `cffi` | `libffi-dev openssl-dev` | — |
| `bcrypt` | `libffi-dev` | — |
| `grpcio` | `linux-headers` | — |
| `opencv-python` | `opencv-dev` | — |
| `matplotlib` | `freetype-dev libpng-dev` | — |
| uv detected | — | `RUN pip install uv` |
| poetry detected | — | `RUN pip install poetry` |

### Rust

Check `Cargo.toml` for crates with native dependencies:

```bash
grep -iE '(openssl|diesel|rusqlite|sqlite|image|ring|pq|mysql|rdkafka|zstd|lz4)' Cargo.toml
```

| Dependency | Extra Packages |
|-----------|----------------|
| `openssl` / `openssl-sys` | `openssl-dev pkgconf` |
| `diesel` (postgres) | `postgresql-dev` |
| `diesel` (mysql) | `mariadb-dev` |
| `rusqlite` / `sqlite` | `sqlite-dev` |
| `image` | `libjpeg-turbo-dev libpng-dev` |
| `rdkafka` | `librdkafka-dev` |
| `zstd` / `lz4` | `zstd-dev lz4-dev` |

### Java/Kotlin

Check `pom.xml` or `build.gradle*` for native dependencies:

```bash
grep -iE '(sqlite-jdbc|postgresql|mysql-connector|netty-transport-native|conscrypt)' \
  pom.xml build.gradle build.gradle.kts 2>/dev/null
```

| Dependency | Extra Packages |
|-----------|----------------|
| `sqlite-jdbc` | `sqlite-dev` |
| `netty-transport-native-epoll` | `linux-headers` |

Most Java dependencies are self-contained JARs — extra packages are rarely needed.

### Ruby

Check `Gemfile` for gems with native extensions:

```bash
grep -iE '(nokogiri|pg|mysql2|sqlite3|rmagick|mini_magick|image_processing|grpc|ffi)' Gemfile
```

| Dependency | Extra Packages |
|-----------|----------------|
| `nokogiri` | `libxml2-dev libxslt-dev` |
| `pg` | `postgresql-dev` |
| `mysql2` | `mariadb-dev` |
| `sqlite3` | `sqlite-dev` |
| `rmagick` | `imagemagick-dev` |
| `mini_magick` / `image_processing` | `imagemagick` |
| `grpc` | `linux-headers` |

### Elixir

Check `mix.exs` for native dependencies:

```bash
grep -iE '(bcrypt_elixir|argon2_elixir|comeonin|sqlite|exqlite|evision)' mix.exs
```

| Dependency | Extra Packages |
|-----------|----------------|
| `bcrypt_elixir` / `comeonin` | none (gcc already in base) |
| `argon2_elixir` | none (gcc already in base) |
| `exqlite` | `sqlite-dev` |
| `evision` | `cmake opencv-dev` |

### C#/.NET

C#/.NET uses NuGet packages that are typically self-contained. Extra packages are rarely needed, but check for:

```bash
grep -iE '(SkiaSharp|ImageSharp|Npgsql|MySqlConnector|Microsoft.Data.Sqlite)' *.csproj 2>/dev/null
```

| Dependency | Extra Packages |
|-----------|----------------|
| `SkiaSharp` | `fontconfig-dev` |
| `Npgsql` | none (managed driver) |
| `Microsoft.Data.Sqlite` | `sqlite-dev` |

### Docker / Testcontainers

Detect if the project uses Docker from within tests (testcontainers) or programmatic Docker access (Docker SDK). This determines whether the ralphex container needs access to the host Docker daemon via socket mounting.

```bash
# Go
grep -iE 'testcontainers-go|github\.com/docker/docker' go.mod go.sum 2>/dev/null

# Node.js
grep -iE '"testcontainers"' package.json 2>/dev/null

# Python
grep -iE 'testcontainers' requirements*.txt pyproject.toml Pipfile setup.py setup.cfg 2>/dev/null

# Java/Kotlin
grep -iE 'org\.testcontainers' pom.xml build.gradle build.gradle.kts 2>/dev/null

# Ruby
grep -iE 'testcontainers' Gemfile 2>/dev/null

# C#/.NET
grep -iE 'Testcontainers' *.csproj 2>/dev/null

# Generic: project Docker files (do NOT auto-trigger socket mounting)
ls Dockerfile docker-compose.yml docker-compose.yaml .dockerignore 2>/dev/null
```

| Detection | Extra Packages | Extra Volumes | Extra Environment |
|-----------|---------------|---------------|-------------------|
| testcontainers (any language) | `docker-cli` | `/var/run/docker.sock:/var/run/docker.sock` | `DOCKER_HOST=unix:///var/run/docker.sock`, `TESTCONTAINERS_RYUK_DISABLED=true`, `TESTCONTAINERS_DOCKER_SOCKET_OVERRIDE=/var/run/docker.sock` |
| Docker SDK only (`github.com/docker/docker`, etc.) | `docker-cli` | `/var/run/docker.sock:/var/run/docker.sock` | `DOCKER_HOST=unix:///var/run/docker.sock` |
| Project Docker files only (Dockerfile, docker-compose.yml) | none | none | none (ask user if they need Docker access for tests) |

**Note:** Project Docker files alone (Dockerfile, docker-compose.yml, .dockerignore) do NOT auto-trigger socket mounting. Only testcontainers or Docker SDK usage triggers automatic Docker socket configuration. If only project Docker files are found, ask the user if they also need Docker access for running tests inside the container.

## Output Format

After running the checks, produce a summary:

- **Extra packages**: combined deduplicated list of `apk add` packages
- **Extra RUN lines**: any additional Dockerfile commands (package manager installs, etc.)
- **Extra volumes**: additional Docker volume mounts (e.g., Docker socket)
- **Extra environment**: additional environment variables (e.g., `DOCKER_HOST`, testcontainers settings)
- **Needs Docker socket**: `true` if testcontainers or Docker SDK detected
- **Needs custom Dockerfile**: `true` if any extra packages or RUN lines were found

If no extra dependencies are found, report "No extra system packages needed."
