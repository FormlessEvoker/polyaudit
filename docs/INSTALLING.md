# Installing polyaudit

## Install with Go

Go 1.26 or newer can install a published version directly:

```sh
go install github.com/FormlessEvoker/polyaudit/cmd/polyaudit@v0.1.0
```

Ensure `$(go env GOPATH)/bin` is on `PATH`, unless you configure `GOBIN`, then
verify the install:

```sh
polyaudit version
```

## Install a release archive

Download the archive and `checksums.txt` for the desired release from
[GitHub Releases](https://github.com/FormlessEvoker/polyaudit/releases). Initial
`0.x` releases appear as prereleases.

For example, after `v0.1.0` is published, on an Apple Silicon Mac:

```sh
curl -fLO https://github.com/FormlessEvoker/polyaudit/releases/download/v0.1.0/polyaudit_0.1.0_darwin_arm64.tar.gz
curl -fLO https://github.com/FormlessEvoker/polyaudit/releases/download/v0.1.0/checksums.txt
shasum -a 256 -c checksums.txt --ignore-missing
mkdir -p "$HOME/.local/bin"
tar -xzf polyaudit_0.1.0_darwin_arm64.tar.gz polyaudit
install -m 755 polyaudit "$HOME/.local/bin/polyaudit"
export PATH="$HOME/.local/bin:$PATH"
polyaudit version
```

Use `darwin_amd64` for Intel Macs, or `linux_amd64` / `linux_arm64` for Linux.
On Linux, verify with `sha256sum --check --ignore-missing checksums.txt`. Add the
`PATH` export to your shell configuration to keep it across sessions.

## Build from source

Clone the repository and build with Go 1.26 or newer:

```sh
make build
./bin/polyaudit version
```

The direct dependencies for Go manifest and Ansible YAML parsing are pinned in
`go.mod` and `go.sum`. For development and release checks, see
[releasing](RELEASING.md).
