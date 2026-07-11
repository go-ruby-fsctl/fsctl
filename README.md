# go-ruby-fsctl/fsctl

[![ci](https://github.com/go-ruby-fsctl/fsctl/actions/workflows/ci.yml/badge.svg)](https://github.com/go-ruby-fsctl/fsctl/actions/workflows/ci.yml)
[![Go Reference](https://pkg.go.dev/badge/github.com/go-ruby-fsctl/fsctl.svg)](https://pkg.go.dev/github.com/go-ruby-fsctl/fsctl)
[![License: BSD-3-Clause](https://img.shields.io/badge/License-BSD--3--Clause-blue.svg)](LICENSE)

The pure-Go, Ruby-runtime-independent core of the Ruby `fsctl` gem: a reflective
adapter over the [go-fsctl](https://github.com/go-fsctl) family of pure-Go Linux
kernel-ioctl wrappers —
[`loop`](https://github.com/go-fsctl/loop),
[`dm`](https://github.com/go-fsctl/dm),
[`btrfs`](https://github.com/go-fsctl/btrfs) and
[`zfs`](https://github.com/go-fsctl/zfs).

A `Session` exposes the most useful operation of each subpackage through typed
methods that return **Ruby-shaped values** (a Hash `map[string]any`, an Array
`[]any`, or a scalar) and through a single dynamic entry point, `Call`, which
maps a Ruby-style snake_case method name to the corresponding operation, coerces
the Ruby arguments, and normalises the result. That uniform surface is what an
[rbgo](https://github.com/go-embedded-ruby) binding drives from `method_missing`;
nothing here depends on the Ruby runtime, so it is equally usable as a
standalone Go library.

```go
s := fsctl.NewSession()
dev, _  := s.Call(ctx, "loop_attach", "/tmp/disk.img")   // "/dev/loop3"
subs, _ := s.Call(ctx, "btrfs_subvolume_list", "/mnt")   // Array of Hashes
_, _    = s.Call(ctx, "zfs_snapshot", "tank", "tank/fs@backup")
```

## Method surface

| Prefix | Methods |
| ------ | ------- |
| `loop_`  | `available`, `attach`, `detach`, `set_capacity`, `status`, `find` |
| `dm_`    | `available`, `version`, `create`, `remove`, `suspend`, `resume`, `info`, `list`, `status`, `table`, `message`, `create_linear` |
| `btrfs_` | `available`, `subvolume_create`, `subvolume_delete`, `snapshot_create`, `subvolume_list`, `subvolume_info`, `subvolume_id`, `is_readonly`, `sync` |
| `zfs_`   | `available`, `pool_names`, `create_filesystem`, `destroy`, `snapshot`, `rename`, `clone`, `rollback`, `holds` |

See [`examples/fsctl_usage.rb`](examples/fsctl_usage.rb) for the intended Ruby usage.

## Platform behaviour

The go-fsctl subpackages implement their kernel operations only on Linux and
ship non-Linux stubs (returning `ErrUnsupported`) for every exported function.
The adapter therefore compiles and runs on darwin, windows and linux alike — the
whole `Session`/`Call` surface exists everywhere — and off Linux the wrapped
operations surface the underlying `ErrUnsupported`. Because that stubbing already
lives in go-fsctl, this adapter needs no build-tag split of its own.

Every wrapped operation is reached through a package-level function seam, so
tests inject fakes returning canned results and errors and exercise every method
and error branch **without root and without a live kernel** on any host OS. The
package holds **100% statement coverage**; the Linux CI job is authoritative for
the coverage gate since that is where the real ioctl code lives.

## License

BSD-3-Clause. See [LICENSE](LICENSE).
