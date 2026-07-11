// SPDX-License-Identifier: BSD-3-Clause
//
// Copyright (c) 2026, the go-ruby-fsctl/fsctl authors

// Package fsctl is the pure-Go, Ruby-runtime-independent core of the Ruby
// `fsctl` gem: a reflective adapter over the go-fsctl family of Linux
// kernel-ioctl wrappers — github.com/go-fsctl/loop, /dm, /btrfs and /zfs —
// shaped so that github.com/go-embedded-ruby/ruby can bind it as
// `require "fsctl"`.
//
// A Session exposes the most useful operations of each go-fsctl subpackage
// through typed methods that return Ruby-shaped values (a Hash
// (map[string]any), an Array ([]any), or a scalar string/int/bool) and through
// a single dynamic entry point, Call, which maps a Ruby-style snake_case method
// name ("loop_attach", "dm_create", "btrfs_subvolume_create", "zfs_snapshot",
// …) to the corresponding operation, coerces the Ruby arguments, and normalises
// the result. That uniform surface is what an rbgo binding drives from
// method_missing; nothing here depends on the Ruby runtime, so it is equally
// usable as a standalone Go library.
//
//	s := fsctl.NewSession()
//	dev, err := s.Call(ctx, "loop_attach", "/tmp/disk.img")   // "/dev/loop3"
//	subs, err := s.Call(ctx, "btrfs_subvolume_list", "/mnt")  // Array of Hashes
//
// # Platform behaviour
//
// The go-fsctl subpackages implement their kernel operations only on Linux and
// ship non-Linux stubs (returning ErrUnsupported) for every exported function,
// so this adapter — and the whole Session/Call surface — compiles and runs on
// darwin, windows and linux alike; off Linux the wrapped operations surface the
// underlying ErrUnsupported. Because that stubbing already lives in go-fsctl,
// the adapter needs no build-tag split of its own.
//
// # Testability
//
// Every wrapped operation is reached through a package-level function seam
// (loopAttach, dmCreate, btrfsSubvolCreate, zfsOpen, …) initialised to the real
// go-fsctl function. Tests swap a seam for a fake that returns canned results
// and errors, so every method and every error branch is exercised without root
// and without a live kernel, on any host OS.
package fsctl

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/go-fsctl/btrfs"
	"github.com/go-fsctl/dm"
	"github.com/go-fsctl/loop"
	"github.com/go-fsctl/zfs"
)

// ---------------------------------------------------------------------------
// Seams: indirection over the go-fsctl entry points. Production uses the real
// functions assigned here; tests swap a var, run, and restore it.
// ---------------------------------------------------------------------------

// loop seams.
var (
	loopAvailable   = loop.Available
	loopAttach      = loop.Attach
	loopDetach      = loop.Detach
	loopSetCapacity = loop.SetCapacity
	loopStatus      = loop.Status
	loopFind        = loop.FindByBacking
)

// dm seams.
var (
	dmAvailable       = dm.Available
	dmVersion         = dm.Version
	dmCreate          = dm.Create
	dmRemove          = dm.Remove
	dmSuspend         = dm.Suspend
	dmResume          = dm.Resume
	dmInfo            = dm.Info
	dmList            = dm.List
	dmStatus          = dm.Status
	dmTableStatus     = dm.TableStatus
	dmMessage         = dm.Message
	dmLinear          = dm.Linear
	dmCreateWithTable = dm.CreateWithTable
)

// btrfs seams.
var (
	btrfsAvailable      = btrfs.Available
	btrfsSubvolCreate   = btrfs.SubvolCreate
	btrfsSubvolDelete   = btrfs.SubvolDelete
	btrfsSnapshotCreate = btrfs.SnapshotCreate
	btrfsListSubvolumes = btrfs.ListSubvolumes
	btrfsGetSubvolInfo  = btrfs.GetSubvolInfo
	btrfsSubvolID       = btrfs.SubvolID
	btrfsIsReadonly     = btrfs.IsReadonly
	btrfsSync           = btrfs.Sync
)

// zfsHandle is the subset of *zfs.Handle the adapter drives. Introducing it as
// an interface lets tests substitute a fake handle for the concrete,
// kernel-backed one that zfs.Open returns.
type zfsHandle interface {
	Close() error
	PoolNames() ([]string, error)
	CreateFilesystem(name string) error
	Destroy(name string, defer_ bool) error
	Snapshot(pool string, fullnames []string) error
	Rename(old, newName string, recursive bool) error
	Clone(snapshot, newFs string, props zfs.Nvlist) error
	Rollback(fs string) (string, error)
	Holds(snapshot string) (map[string]uint64, error)
}

// zfs seams.
var (
	zfsAvailable = zfs.Available
	zfsOpen      = func() (zfsHandle, error) { return zfs.Open() }
)

// ---------------------------------------------------------------------------
// Session
// ---------------------------------------------------------------------------

// Session is a Ruby-facing handle over the go-fsctl family. It is stateless:
// each operation opens whatever kernel resource it needs and releases it before
// returning.
type Session struct{}

// NewSession builds a Session.
func NewSession() *Session { return &Session{} }

// --- loop -------------------------------------------------------------------

// LoopAvailable reports whether the loop-control interface is present.
func (s *Session) LoopAvailable() bool { return loopAvailable() }

// LoopAttach binds backingFile to the first free loop device and returns its
// path (e.g. "/dev/loop3"). opt, when present, is a Ruby Hash configuring the
// attachment (offset, size_limit, read_only, autoclear, part_scan).
func (s *Session) LoopAttach(backingFile string, opt map[string]any) (string, error) {
	return loopAttach(backingFile, loopOptions(opt))
}

// LoopDetach unbinds the loop device at dev.
func (s *Session) LoopDetach(dev string) error { return loopDetach(dev) }

// LoopSetCapacity makes the loop device at dev re-read its backing file size.
func (s *Session) LoopSetCapacity(dev string) error { return loopSetCapacity(dev) }

// LoopStatus returns the configuration of the loop device at dev as a Hash.
func (s *Session) LoopStatus(dev string) (map[string]any, error) {
	info, err := loopStatus(dev)
	if err != nil {
		return nil, err
	}
	return toHash(info)
}

// LoopFind returns the loop device paths backed by file, as an Array of strings.
func (s *Session) LoopFind(file string) ([]any, error) {
	devs, err := loopFind(file)
	if err != nil {
		return nil, err
	}
	return toStringArray(devs), nil
}

// loopOptions maps a Ruby options Hash to loop.Options.
func loopOptions(opt map[string]any) loop.Options {
	return loop.Options{
		Offset:    hashUint64(opt, "offset"),
		SizeLimit: hashUint64(opt, "size_limit"),
		ReadOnly:  hashBool(opt, "read_only"),
		Autoclear: hashBool(opt, "autoclear"),
		PartScan:  hashBool(opt, "part_scan"),
	}
}

// --- dm ---------------------------------------------------------------------

// DMAvailable reports whether the device-mapper control interface is present.
func (s *Session) DMAvailable() bool { return dmAvailable() }

// DMVersion returns the device-mapper interface version as a Hash.
func (s *Session) DMVersion() (map[string]any, error) {
	v, err := dmVersion()
	if err != nil {
		return nil, err
	}
	return toHash(v)
}

// DMCreate creates an empty device-mapper device named name with an optional uuid.
func (s *Session) DMCreate(name, uuid string) error { return dmCreate(name, uuid) }

// DMRemove removes the device-mapper device named name.
func (s *Session) DMRemove(name string) error { return dmRemove(name) }

// DMSuspend suspends the device-mapper device named name.
func (s *Session) DMSuspend(name string) error { return dmSuspend(name) }

// DMResume resumes the device-mapper device named name.
func (s *Session) DMResume(name string) error { return dmResume(name) }

// DMInfo returns kernel info for the device-mapper device named name as a Hash.
func (s *Session) DMInfo(name string) (map[string]any, error) {
	info, err := dmInfo(name)
	if err != nil {
		return nil, err
	}
	return toHash(info)
}

// DMList returns all device-mapper devices as an Array of Hashes.
func (s *Session) DMList() ([]any, error) {
	devs, err := dmList()
	if err != nil {
		return nil, err
	}
	return toArray(devs)
}

// DMStatus returns the per-target status rows of name as an Array of Hashes.
func (s *Session) DMStatus(name string) ([]any, error) {
	targets, err := dmStatus(name)
	if err != nil {
		return nil, err
	}
	return toArray(targets)
}

// DMTable returns the live table rows of name as an Array of Hashes.
func (s *Session) DMTable(name string) ([]any, error) {
	targets, err := dmTableStatus(name)
	if err != nil {
		return nil, err
	}
	return toArray(targets)
}

// DMMessage sends a device-mapper message to a target of name at sector and
// returns the target's reply string.
func (s *Session) DMMessage(name string, sector uint64, msg string) (string, error) {
	return dmMessage(name, sector, msg)
}

// DMCreateLinear creates name mapping length 512-byte sectors starting at start
// onto dev at devOffset via a single "linear" target.
func (s *Session) DMCreateLinear(name string, start, length uint64, dev string, devOffset uint64) error {
	return dmCreateWithTable(name, []dm.Target{dmLinear(start, length, dev, devOffset)})
}

// --- btrfs ------------------------------------------------------------------

// BtrfsAvailable reports whether path lives on a btrfs filesystem.
func (s *Session) BtrfsAvailable(path string) bool { return btrfsAvailable(path) }

// BtrfsSubvolumeCreate creates subvolume name under parentDir.
func (s *Session) BtrfsSubvolumeCreate(parentDir, name string) error {
	return btrfsSubvolCreate(parentDir, name)
}

// BtrfsSubvolumeDelete deletes subvolume name under parentDir.
func (s *Session) BtrfsSubvolumeDelete(parentDir, name string) error {
	return btrfsSubvolDelete(parentDir, name)
}

// BtrfsSnapshotCreate snapshots src into destParent as name; readonly makes the
// snapshot read-only.
func (s *Session) BtrfsSnapshotCreate(src, destParent, name string, readonly bool) error {
	return btrfsSnapshotCreate(src, destParent, name, readonly)
}

// BtrfsSubvolumeList returns the subvolumes reachable from path as an Array of
// Hashes.
func (s *Session) BtrfsSubvolumeList(path string) ([]any, error) {
	subs, err := btrfsListSubvolumes(path)
	if err != nil {
		return nil, err
	}
	return toArray(subs)
}

// BtrfsSubvolumeInfo returns the decoded subvolume info for path as a Hash.
func (s *Session) BtrfsSubvolumeInfo(path string) (map[string]any, error) {
	info, err := btrfsGetSubvolInfo(path)
	if err != nil {
		return nil, err
	}
	return toHash(info)
}

// BtrfsSubvolumeID returns the subvolume ID of path.
func (s *Session) BtrfsSubvolumeID(path string) (uint64, error) { return btrfsSubvolID(path) }

// BtrfsIsReadonly reports whether the subvolume at path is read-only.
func (s *Session) BtrfsIsReadonly(path string) (bool, error) { return btrfsIsReadonly(path) }

// BtrfsSync forces a commit of the btrfs filesystem containing path.
func (s *Session) BtrfsSync(path string) error { return btrfsSync(path) }

// --- zfs --------------------------------------------------------------------

// ZfsAvailable reports whether the /dev/zfs control device is present.
func (s *Session) ZfsAvailable() bool { return zfsAvailable() }

// withZFS opens a ZFS handle, runs fn against it, and closes the handle.
func (s *Session) withZFS(fn func(zfsHandle) (any, error)) (any, error) {
	h, err := zfsOpen()
	if err != nil {
		return nil, err
	}
	defer h.Close()
	return fn(h)
}

// ZfsPoolNames returns the imported pool names as an Array of strings.
func (s *Session) ZfsPoolNames() (any, error) {
	return s.withZFS(func(h zfsHandle) (any, error) {
		names, err := h.PoolNames()
		if err != nil {
			return nil, err
		}
		return toStringArray(names), nil
	})
}

// ZfsCreateFilesystem creates the ZFS filesystem name.
func (s *Session) ZfsCreateFilesystem(name string) (any, error) {
	return s.withZFS(func(h zfsHandle) (any, error) {
		return nil, h.CreateFilesystem(name)
	})
}

// ZfsDestroy destroys the ZFS dataset name; deferred defers destruction while
// the dataset is held.
func (s *Session) ZfsDestroy(name string, deferred bool) (any, error) {
	return s.withZFS(func(h zfsHandle) (any, error) {
		return nil, h.Destroy(name, deferred)
	})
}

// ZfsSnapshot atomically creates the named snapshots (fully-qualified
// "pool/fs@snap") within pool.
func (s *Session) ZfsSnapshot(pool string, fullnames []string) (any, error) {
	return s.withZFS(func(h zfsHandle) (any, error) {
		return nil, h.Snapshot(pool, fullnames)
	})
}

// ZfsRename renames dataset old to newName; recursive renames descendant
// snapshots.
func (s *Session) ZfsRename(old, newName string, recursive bool) (any, error) {
	return s.withZFS(func(h zfsHandle) (any, error) {
		return nil, h.Rename(old, newName, recursive)
	})
}

// ZfsClone clones snapshot into the new filesystem newFs.
func (s *Session) ZfsClone(snapshot, newFs string) (any, error) {
	return s.withZFS(func(h zfsHandle) (any, error) {
		return nil, h.Clone(snapshot, newFs, nil)
	})
}

// ZfsRollback rolls fs back to its most recent snapshot and returns that
// snapshot's name.
func (s *Session) ZfsRollback(fs string) (any, error) {
	return s.withZFS(func(h zfsHandle) (any, error) {
		return h.Rollback(fs)
	})
}

// ZfsHolds returns the user holds on snapshot as a Hash of tag -> creation time.
func (s *Session) ZfsHolds(snapshot string) (any, error) {
	return s.withZFS(func(h zfsHandle) (any, error) {
		holds, err := h.Holds(snapshot)
		if err != nil {
			return nil, err
		}
		out := make(map[string]any, len(holds))
		for k, v := range holds {
			out[k] = v
		}
		return out, nil
	})
}

// ---------------------------------------------------------------------------
// Reflective dispatch
// ---------------------------------------------------------------------------

// Call is the dynamic dispatch entry point an rbgo binding uses. It routes a
// snake_case method name to the matching Session operation and coerces the
// Ruby-supplied arguments. The ctx is accepted for interface symmetry with
// other go-ruby adapters; the go-fsctl operations are synchronous ioctls and do
// not consume it. Unknown methods return an error naming the method.
func (s *Session) Call(ctx context.Context, method string, args ...any) (any, error) {
	_ = ctx
	switch strings.ToLower(method) {
	// loop
	case "loop_available":
		return s.LoopAvailable(), nil
	case "loop_attach":
		if len(args) < 1 {
			return nil, argErr(method, "a backing file")
		}
		return s.LoopAttach(toString(args[0]), argHash(args, 1))
	case "loop_detach":
		if len(args) < 1 {
			return nil, argErr(method, "a device path")
		}
		return nil, s.LoopDetach(toString(args[0]))
	case "loop_set_capacity":
		if len(args) < 1 {
			return nil, argErr(method, "a device path")
		}
		return nil, s.LoopSetCapacity(toString(args[0]))
	case "loop_status":
		if len(args) < 1 {
			return nil, argErr(method, "a device path")
		}
		return s.LoopStatus(toString(args[0]))
	case "loop_find":
		if len(args) < 1 {
			return nil, argErr(method, "a backing file")
		}
		return s.LoopFind(toString(args[0]))

	// dm
	case "dm_available":
		return s.DMAvailable(), nil
	case "dm_version":
		return s.DMVersion()
	case "dm_create":
		if len(args) < 1 {
			return nil, argErr(method, "a device name")
		}
		return nil, s.DMCreate(toString(args[0]), argString(args, 1, ""))
	case "dm_remove":
		if len(args) < 1 {
			return nil, argErr(method, "a device name")
		}
		return nil, s.DMRemove(toString(args[0]))
	case "dm_suspend":
		if len(args) < 1 {
			return nil, argErr(method, "a device name")
		}
		return nil, s.DMSuspend(toString(args[0]))
	case "dm_resume":
		if len(args) < 1 {
			return nil, argErr(method, "a device name")
		}
		return nil, s.DMResume(toString(args[0]))
	case "dm_info":
		if len(args) < 1 {
			return nil, argErr(method, "a device name")
		}
		return s.DMInfo(toString(args[0]))
	case "dm_list":
		return s.DMList()
	case "dm_status":
		if len(args) < 1 {
			return nil, argErr(method, "a device name")
		}
		return s.DMStatus(toString(args[0]))
	case "dm_table":
		if len(args) < 1 {
			return nil, argErr(method, "a device name")
		}
		return s.DMTable(toString(args[0]))
	case "dm_message":
		if len(args) < 3 {
			return nil, argErr(method, "a name, sector and message")
		}
		return s.DMMessage(toString(args[0]), toUint64(args[1]), toString(args[2]))
	case "dm_create_linear":
		if len(args) < 5 {
			return nil, argErr(method, "a name, start, length, device and offset")
		}
		return nil, s.DMCreateLinear(toString(args[0]), toUint64(args[1]), toUint64(args[2]), toString(args[3]), toUint64(args[4]))

	// btrfs
	case "btrfs_available":
		if len(args) < 1 {
			return nil, argErr(method, "a path")
		}
		return s.BtrfsAvailable(toString(args[0])), nil
	case "btrfs_subvolume_create":
		if len(args) < 2 {
			return nil, argErr(method, "a parent dir and name")
		}
		return nil, s.BtrfsSubvolumeCreate(toString(args[0]), toString(args[1]))
	case "btrfs_subvolume_delete":
		if len(args) < 2 {
			return nil, argErr(method, "a parent dir and name")
		}
		return nil, s.BtrfsSubvolumeDelete(toString(args[0]), toString(args[1]))
	case "btrfs_snapshot_create":
		if len(args) < 3 {
			return nil, argErr(method, "a source, dest parent and name")
		}
		return nil, s.BtrfsSnapshotCreate(toString(args[0]), toString(args[1]), toString(args[2]), argBool(args, 3, false))
	case "btrfs_subvolume_list":
		if len(args) < 1 {
			return nil, argErr(method, "a path")
		}
		return s.BtrfsSubvolumeList(toString(args[0]))
	case "btrfs_subvolume_info":
		if len(args) < 1 {
			return nil, argErr(method, "a path")
		}
		return s.BtrfsSubvolumeInfo(toString(args[0]))
	case "btrfs_subvolume_id":
		if len(args) < 1 {
			return nil, argErr(method, "a path")
		}
		return s.BtrfsSubvolumeID(toString(args[0]))
	case "btrfs_is_readonly":
		if len(args) < 1 {
			return nil, argErr(method, "a path")
		}
		return s.BtrfsIsReadonly(toString(args[0]))
	case "btrfs_sync":
		if len(args) < 1 {
			return nil, argErr(method, "a path")
		}
		return nil, s.BtrfsSync(toString(args[0]))

	// zfs
	case "zfs_available":
		return s.ZfsAvailable(), nil
	case "zfs_pool_names":
		return s.ZfsPoolNames()
	case "zfs_create_filesystem":
		if len(args) < 1 {
			return nil, argErr(method, "a filesystem name")
		}
		return s.ZfsCreateFilesystem(toString(args[0]))
	case "zfs_destroy":
		if len(args) < 1 {
			return nil, argErr(method, "a dataset name")
		}
		return s.ZfsDestroy(toString(args[0]), argBool(args, 1, false))
	case "zfs_snapshot":
		if len(args) < 2 {
			return nil, argErr(method, "a pool and at least one snapshot name")
		}
		return s.ZfsSnapshot(toString(args[0]), toStrings(args[1:]))
	case "zfs_rename":
		if len(args) < 2 {
			return nil, argErr(method, "an old and new name")
		}
		return s.ZfsRename(toString(args[0]), toString(args[1]), argBool(args, 2, false))
	case "zfs_clone":
		if len(args) < 2 {
			return nil, argErr(method, "a snapshot and new filesystem")
		}
		return s.ZfsClone(toString(args[0]), toString(args[1]))
	case "zfs_rollback":
		if len(args) < 1 {
			return nil, argErr(method, "a filesystem name")
		}
		return s.ZfsRollback(toString(args[0]))
	case "zfs_holds":
		if len(args) < 1 {
			return nil, argErr(method, "a snapshot name")
		}
		return s.ZfsHolds(toString(args[0]))

	default:
		return nil, fmt.Errorf("fsctl: unknown method %q", method)
	}
}

func argErr(method, need string) error {
	return fmt.Errorf("fsctl: %q requires %s", method, need)
}

// ---------------------------------------------------------------------------
// Ruby value coercion
// ---------------------------------------------------------------------------

// toHash normalises a typed value (struct or pointer) into a Ruby Hash via a
// JSON round-trip, matching the exported field names go-fsctl uses.
func toHash(v any) (map[string]any, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	var out map[string]any
	if err := json.Unmarshal(b, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// toArray normalises a typed slice into a Ruby Array of Hashes via a JSON
// round-trip.
func toArray(v any) ([]any, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	var out []any
	if err := json.Unmarshal(b, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// toStringArray widens a []string into a Ruby Array ([]any).
func toStringArray(ss []string) []any {
	out := make([]any, len(ss))
	for i, s := range ss {
		out[i] = s
	}
	return out
}

// toStrings coerces a slice of Ruby-supplied args to []string.
func toStrings(args []any) []string {
	out := make([]string, len(args))
	for i, a := range args {
		out[i] = toString(a)
	}
	return out
}

// toString coerces a Ruby-supplied argument to a string.
func toString(v any) string {
	switch t := v.(type) {
	case string:
		return t
	case fmt.Stringer:
		return t.String()
	case nil:
		return ""
	default:
		return fmt.Sprintf("%v", t)
	}
}

// toUint64 coerces a Ruby-supplied numeric argument to a uint64.
func toUint64(v any) uint64 {
	switch t := v.(type) {
	case uint64:
		return t
	case int:
		return uint64(t)
	case int64:
		return uint64(t)
	case float64:
		return uint64(t)
	default:
		return 0
	}
}

// toBool coerces a Ruby-supplied argument to a bool.
func toBool(v any) bool {
	switch t := v.(type) {
	case bool:
		return t
	case nil:
		return false
	default:
		return false
	}
}

// argString returns the i-th arg as a string, or def when absent/empty.
func argString(args []any, i int, def string) string {
	if i >= len(args) {
		return def
	}
	if s := toString(args[i]); s != "" {
		return s
	}
	return def
}

// argBool returns the i-th arg as a bool, or def when absent.
func argBool(args []any, i int, def bool) bool {
	if i >= len(args) {
		return def
	}
	return toBool(args[i])
}

// argHash returns the i-th arg as a Ruby Hash, or nil when absent or not a Hash.
func argHash(args []any, i int) map[string]any {
	if i >= len(args) {
		return nil
	}
	if h, ok := args[i].(map[string]any); ok {
		return h
	}
	return nil
}

// hashUint64 reads key from a Ruby options Hash as a uint64 (0 when absent).
func hashUint64(h map[string]any, key string) uint64 {
	if h == nil {
		return 0
	}
	return toUint64(h[key])
}

// hashBool reads key from a Ruby options Hash as a bool (false when absent).
func hashBool(h map[string]any, key string) bool {
	if h == nil {
		return false
	}
	return toBool(h[key])
}
