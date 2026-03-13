# ralphex-dk - Docker Wrapper

Python wrapper script that runs ralphex inside a Docker container, handling credential management, volume mounts, and environment configuration.

## Files

- `ralphex_dk.py` - symlink to `../ralphex-dk.sh` for Python test imports
- `ralphex_dk_test.py` - unit tests (~1900 lines, 151 tests)
- `../ralphex-dk.sh` - actual wrapper script (~1000 lines), served by curl install URL

## Usage

```bash
python3 scripts/ralphex-dk.sh [wrapper-flags] [ralphex-args]
```

### Wrapper flags

- `-E, --env VAR[=val]` - extra env var to pass to container (repeatable)
- `-v, --volume src:dst[:opts]` - extra volume mount (repeatable)
- `--update` - pull latest Docker image and exit
- `--update-script` - update this wrapper script and exit
- `--test` - run unit tests and exit
- `-h, --help` - show help
- `--claude-provider PROVIDER` - claude provider: `default` or `bedrock`

## Running Tests

```bash
python3 scripts/ralphex-dk.sh --test
```

## Installation (curl)

```bash
curl -sL https://raw.githubusercontent.com/umputun/ralphex/master/scripts/ralphex-dk.sh -o /usr/local/bin/ralphex
chmod +x /usr/local/bin/ralphex
```

`scripts/ralphex-dk.sh` is the actual file, keeping this install URL stable. `ralphex_dk.py` is a symlink back to it for Python test imports.
