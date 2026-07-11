// SPDX-License-Identifier: BSD-3-Clause
//
// Copyright (c) 2026, the go-ruby-fsctl/fsctl authors

package fsctl

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/go-fsctl/btrfs"
	"github.com/go-fsctl/dm"
	"github.com/go-fsctl/loop"
	"github.com/go-fsctl/zfs"
)

var errBoom = errors.New("boom")

// swap replaces *p with v and returns a restore func for defer.
func swap[T any](p *T, v T) func() {
	old := *p
	*p = v
	return func() { *p = old }
}

// ---------------------------------------------------------------------------
// fakeHandle implements zfsHandle with per-method injected results.
// ---------------------------------------------------------------------------

type fakeHandle struct {
	closed     bool
	err        error             // returned by every op unless a specific value is set
	poolNames  []string          // PoolNames success value
	rollbackTo string            // Rollback success value
	holds      map[string]uint64 // Holds success value
}

func (h *fakeHandle) Close() error { h.closed = true; return nil }
func (h *fakeHandle) PoolNames() ([]string, error) {
	if h.err != nil {
		return nil, h.err
	}
	return h.poolNames, nil
}
func (h *fakeHandle) CreateFilesystem(string) error          { return h.err }
func (h *fakeHandle) Destroy(string, bool) error             { return h.err }
func (h *fakeHandle) Snapshot(string, []string) error        { return h.err }
func (h *fakeHandle) Rename(string, string, bool) error      { return h.err }
func (h *fakeHandle) Clone(string, string, zfs.Nvlist) error { return h.err }
func (h *fakeHandle) Rollback(string) (string, error) {
	if h.err != nil {
		return "", h.err
	}
	return h.rollbackTo, nil
}
func (h *fakeHandle) Holds(string) (map[string]uint64, error) {
	if h.err != nil {
		return nil, h.err
	}
	return h.holds, nil
}

// useHandle installs a fakeHandle for zfsOpen and returns it plus the restore.
func useHandle(h *fakeHandle) func() {
	return swap(&zfsOpen, func() (zfsHandle, error) { return h, nil })
}

// ---------------------------------------------------------------------------
// loop
// ---------------------------------------------------------------------------

func TestLoop(t *testing.T) {
	s := NewSession()

	defer swap(&loopAvailable, func() bool { return true })()
	if !s.LoopAvailable() {
		t.Error("LoopAvailable")
	}

	// Attach: success (with options Hash) then error.
	defer swap(&loopAttach, func(f string, o loop.Options) (string, error) {
		if f != "/img" || o.Offset != 512 || !o.ReadOnly || o.SizeLimit != 4096 || !o.Autoclear || !o.PartScan {
			t.Errorf("attach args f=%q o=%+v", f, o)
		}
		return "/dev/loop7", nil
	})()
	dev, err := s.LoopAttach("/img", map[string]any{
		"offset": int64(512), "size_limit": int64(4096),
		"read_only": true, "autoclear": true, "part_scan": true,
	})
	if err != nil || dev != "/dev/loop7" {
		t.Fatalf("LoopAttach = %q, %v", dev, err)
	}

	// Detach / SetCapacity success + error.
	defer swap(&loopDetach, func(string) error { return nil })()
	if err := s.LoopDetach("/dev/loop7"); err != nil {
		t.Fatal(err)
	}
	defer swap(&loopSetCapacity, func(string) error { return errBoom })()
	if err := s.LoopSetCapacity("/dev/loop7"); err != errBoom {
		t.Fatalf("LoopSetCapacity err = %v", err)
	}

	// Status success then error.
	defer swap(&loopStatus, func(string) (loop.Info, error) {
		return loop.Info{Number: 7, BackingFile: "/img"}, nil
	})()
	h, err := s.LoopStatus("/dev/loop7")
	if err != nil {
		t.Fatal(err)
	}
	if h["BackingFile"] != "/img" || h["Number"].(float64) != 7 {
		t.Errorf("LoopStatus = %v", h)
	}
	swap(&loopStatus, func(string) (loop.Info, error) { return loop.Info{}, errBoom })
	if _, err := s.LoopStatus("/dev/loop7"); err != errBoom {
		t.Errorf("LoopStatus err = %v", err)
	}

	// Find success then error.
	defer swap(&loopFind, func(string) ([]string, error) { return []string{"/dev/loop7"}, nil })()
	arr, err := s.LoopFind("/img")
	if err != nil || len(arr) != 1 || arr[0] != "/dev/loop7" {
		t.Errorf("LoopFind = %v, %v", arr, err)
	}
	swap(&loopFind, func(string) ([]string, error) { return nil, errBoom })
	if _, err := s.LoopFind("/img"); err != errBoom {
		t.Errorf("LoopFind err = %v", err)
	}
}

// ---------------------------------------------------------------------------
// dm
// ---------------------------------------------------------------------------

func TestDM(t *testing.T) {
	s := NewSession()

	defer swap(&dmAvailable, func() bool { return true })()
	if !s.DMAvailable() {
		t.Error("DMAvailable")
	}

	// Version success + error.
	defer swap(&dmVersion, func() (dm.DMVersion, error) {
		return dm.DMVersion{Major: 4, Minor: 48, Patch: 0}, nil
	})()
	if v, err := s.DMVersion(); err != nil || v["Major"].(float64) != 4 {
		t.Errorf("DMVersion = %v, %v", v, err)
	}
	swap(&dmVersion, func() (dm.DMVersion, error) { return dm.DMVersion{}, errBoom })
	if _, err := s.DMVersion(); err != errBoom {
		t.Errorf("DMVersion err = %v", err)
	}

	// Create/Remove/Suspend/Resume (success on all, plus one error each path).
	defer swap(&dmCreate, func(n, u string) error {
		if n != "d" || u != "u1" {
			t.Errorf("dmCreate(%q,%q)", n, u)
		}
		return nil
	})()
	if err := s.DMCreate("d", "u1"); err != nil {
		t.Fatal(err)
	}
	defer swap(&dmRemove, func(string) error { return nil })()
	if err := s.DMRemove("d"); err != nil {
		t.Fatal(err)
	}
	defer swap(&dmSuspend, func(string) error { return nil })()
	if err := s.DMSuspend("d"); err != nil {
		t.Fatal(err)
	}
	defer swap(&dmResume, func(string) error { return errBoom })()
	if err := s.DMResume("d"); err != errBoom {
		t.Fatal(err)
	}

	// Info success + error.
	defer swap(&dmInfo, func(string) (dm.DevInfo, error) {
		return dm.DevInfo{Name: "d", TargetCnt: 1}, nil
	})()
	if h, err := s.DMInfo("d"); err != nil || h["Name"] != "d" {
		t.Errorf("DMInfo = %v, %v", h, err)
	}
	swap(&dmInfo, func(string) (dm.DevInfo, error) { return dm.DevInfo{}, errBoom })
	if _, err := s.DMInfo("d"); err != errBoom {
		t.Errorf("DMInfo err = %v", err)
	}

	// List success + error.
	defer swap(&dmList, func() ([]dm.Device, error) {
		return []dm.Device{{Name: "d", Dev: 1}}, nil
	})()
	if a, err := s.DMList(); err != nil || len(a) != 1 {
		t.Errorf("DMList = %v, %v", a, err)
	}
	swap(&dmList, func() ([]dm.Device, error) { return nil, errBoom })
	if _, err := s.DMList(); err != errBoom {
		t.Errorf("DMList err = %v", err)
	}

	// Status success + error.
	defer swap(&dmStatus, func(string) ([]dm.Target, error) {
		return []dm.Target{{Type: "linear"}}, nil
	})()
	if a, err := s.DMStatus("d"); err != nil || len(a) != 1 {
		t.Errorf("DMStatus = %v, %v", a, err)
	}
	swap(&dmStatus, func(string) ([]dm.Target, error) { return nil, errBoom })
	if _, err := s.DMStatus("d"); err != errBoom {
		t.Errorf("DMStatus err = %v", err)
	}

	// Table success + error.
	defer swap(&dmTableStatus, func(string) ([]dm.Target, error) {
		return []dm.Target{{Type: "linear"}}, nil
	})()
	if a, err := s.DMTable("d"); err != nil || len(a) != 1 {
		t.Errorf("DMTable = %v, %v", a, err)
	}
	swap(&dmTableStatus, func(string) ([]dm.Target, error) { return nil, errBoom })
	if _, err := s.DMTable("d"); err != errBoom {
		t.Errorf("DMTable err = %v", err)
	}

	// Message.
	defer swap(&dmMessage, func(n string, sec uint64, m string) (string, error) {
		if n != "d" || sec != 0 || m != "ping" {
			t.Errorf("dmMessage(%q,%d,%q)", n, sec, m)
		}
		return "pong", nil
	})()
	if r, err := s.DMMessage("d", 0, "ping"); err != nil || r != "pong" {
		t.Errorf("DMMessage = %q, %v", r, err)
	}

	// CreateLinear: check Linear target is threaded to CreateWithTable.
	linearHit := false
	defer swap(&dmLinear, func(start, length uint64, dev string, off uint64) dm.Target {
		linearHit = true
		return dm.Target{SectorStart: start, Length: length, Type: "linear", Params: dev}
	})()
	defer swap(&dmCreateWithTable, func(name string, ts []dm.Target) error {
		if name != "d" || len(ts) != 1 || ts[0].Type != "linear" {
			t.Errorf("dmCreateWithTable(%q,%v)", name, ts)
		}
		return nil
	})()
	if err := s.DMCreateLinear("d", 0, 2048, "/dev/sda", 0); err != nil || !linearHit {
		t.Errorf("DMCreateLinear err=%v linearHit=%v", err, linearHit)
	}
}

// ---------------------------------------------------------------------------
// btrfs
// ---------------------------------------------------------------------------

func TestBtrfs(t *testing.T) {
	s := NewSession()

	defer swap(&btrfsAvailable, func(string) bool { return true })()
	if !s.BtrfsAvailable("/mnt") {
		t.Error("BtrfsAvailable")
	}

	defer swap(&btrfsSubvolCreate, func(p, n string) error {
		if p != "/mnt" || n != "sv" {
			t.Errorf("SubvolCreate(%q,%q)", p, n)
		}
		return nil
	})()
	if err := s.BtrfsSubvolumeCreate("/mnt", "sv"); err != nil {
		t.Fatal(err)
	}
	defer swap(&btrfsSubvolDelete, func(string, string) error { return nil })()
	if err := s.BtrfsSubvolumeDelete("/mnt", "sv"); err != nil {
		t.Fatal(err)
	}
	defer swap(&btrfsSnapshotCreate, func(src, dp, n string, ro bool) error {
		if !ro {
			t.Error("expected readonly snapshot")
		}
		return nil
	})()
	if err := s.BtrfsSnapshotCreate("/mnt/sv", "/mnt", "snap", true); err != nil {
		t.Fatal(err)
	}

	// List success + error.
	defer swap(&btrfsListSubvolumes, func(string) ([]btrfs.Subvolume, error) {
		return []btrfs.Subvolume{{ID: 256, Name: "sv"}}, nil
	})()
	if a, err := s.BtrfsSubvolumeList("/mnt"); err != nil || len(a) != 1 {
		t.Errorf("BtrfsSubvolumeList = %v, %v", a, err)
	}
	swap(&btrfsListSubvolumes, func(string) ([]btrfs.Subvolume, error) { return nil, errBoom })
	if _, err := s.BtrfsSubvolumeList("/mnt"); err != errBoom {
		t.Errorf("BtrfsSubvolumeList err = %v", err)
	}

	// Info success + error.
	defer swap(&btrfsGetSubvolInfo, func(string) (*btrfs.SubvolInfo, error) {
		return &btrfs.SubvolInfo{ID: 256, Name: "sv"}, nil
	})()
	if h, err := s.BtrfsSubvolumeInfo("/mnt/sv"); err != nil || h["Name"] != "sv" {
		t.Errorf("BtrfsSubvolumeInfo = %v, %v", h, err)
	}
	swap(&btrfsGetSubvolInfo, func(string) (*btrfs.SubvolInfo, error) { return nil, errBoom })
	if _, err := s.BtrfsSubvolumeInfo("/mnt/sv"); err != errBoom {
		t.Errorf("BtrfsSubvolumeInfo err = %v", err)
	}

	// ID + IsReadonly + Sync (scalars/error passthrough).
	defer swap(&btrfsSubvolID, func(string) (uint64, error) { return 256, nil })()
	if id, err := s.BtrfsSubvolumeID("/mnt/sv"); err != nil || id != 256 {
		t.Errorf("BtrfsSubvolumeID = %d, %v", id, err)
	}
	defer swap(&btrfsIsReadonly, func(string) (bool, error) { return true, nil })()
	if ro, err := s.BtrfsIsReadonly("/mnt/sv"); err != nil || !ro {
		t.Errorf("BtrfsIsReadonly = %v, %v", ro, err)
	}
	defer swap(&btrfsSync, func(string) error { return errBoom })()
	if err := s.BtrfsSync("/mnt"); err != errBoom {
		t.Errorf("BtrfsSync err = %v", err)
	}
}

// ---------------------------------------------------------------------------
// zfs
// ---------------------------------------------------------------------------

func TestZfs(t *testing.T) {
	s := NewSession()

	defer swap(&zfsAvailable, func() bool { return true })()
	if !s.ZfsAvailable() {
		t.Error("ZfsAvailable")
	}

	// Open-error branch (shared withZFS): default zfsOpen off Linux errors;
	// force it explicitly so the test is OS-independent.
	defer swap(&zfsOpen, func() (zfsHandle, error) { return nil, errBoom })()
	if _, err := s.ZfsPoolNames(); err != errBoom {
		t.Errorf("open error not propagated: %v", err)
	}

	// PoolNames success + op error.
	h := &fakeHandle{poolNames: []string{"tank", "rpool"}}
	restore := useHandle(h)
	names, err := s.ZfsPoolNames()
	if err != nil {
		t.Fatal(err)
	}
	if arr := names.([]any); len(arr) != 2 || arr[0] != "tank" {
		t.Errorf("ZfsPoolNames = %v", names)
	}
	if !h.closed {
		t.Error("handle not closed")
	}
	restore()

	// Op-error for each write method via a failing handle.
	hErr := &fakeHandle{err: errBoom}
	restore = useHandle(hErr)
	for name, fn := range map[string]func() (any, error){
		"pool_names":        s.ZfsPoolNames,
		"create_filesystem": func() (any, error) { return s.ZfsCreateFilesystem("tank/fs") },
		"destroy":           func() (any, error) { return s.ZfsDestroy("tank/fs", true) },
		"snapshot":          func() (any, error) { return s.ZfsSnapshot("tank", []string{"tank/fs@s"}) },
		"rename":            func() (any, error) { return s.ZfsRename("tank/a", "tank/b", true) },
		"clone":             func() (any, error) { return s.ZfsClone("tank/fs@s", "tank/c") },
		"rollback":          func() (any, error) { return s.ZfsRollback("tank/fs") },
		"holds":             func() (any, error) { return s.ZfsHolds("tank/fs@s") },
	} {
		if _, err := fn(); err != errBoom {
			t.Errorf("Zfs %s error = %v", name, err)
		}
	}
	restore()

	// Success paths for the write/read methods.
	hOK := &fakeHandle{rollbackTo: "tank/fs@s1", holds: map[string]uint64{"keep": 1700000000}}
	defer useHandle(hOK)()
	if _, err := s.ZfsCreateFilesystem("tank/fs"); err != nil {
		t.Error(err)
	}
	if _, err := s.ZfsDestroy("tank/fs", false); err != nil {
		t.Error(err)
	}
	if _, err := s.ZfsSnapshot("tank", []string{"tank/fs@s"}); err != nil {
		t.Error(err)
	}
	if _, err := s.ZfsRename("tank/a", "tank/b", false); err != nil {
		t.Error(err)
	}
	if _, err := s.ZfsClone("tank/fs@s", "tank/c"); err != nil {
		t.Error(err)
	}
	if rb, err := s.ZfsRollback("tank/fs"); err != nil || rb != "tank/fs@s1" {
		t.Errorf("ZfsRollback = %v, %v", rb, err)
	}
	holds, err := s.ZfsHolds("tank/fs@s")
	if err != nil {
		t.Fatal(err)
	}
	if m := holds.(map[string]any); m["keep"].(uint64) != 1700000000 {
		t.Errorf("ZfsHolds = %v", holds)
	}
}

// ---------------------------------------------------------------------------
// Call dispatch
// ---------------------------------------------------------------------------

func TestCallDispatch(t *testing.T) {
	s := NewSession()
	ctx := context.Background()

	// Stub every seam so dispatch reaches a deterministic result.
	defer swap(&loopAvailable, func() bool { return true })()
	defer swap(&loopAttach, func(string, loop.Options) (string, error) { return "/dev/loop0", nil })()
	defer swap(&loopDetach, func(string) error { return nil })()
	defer swap(&loopSetCapacity, func(string) error { return nil })()
	defer swap(&loopStatus, func(string) (loop.Info, error) { return loop.Info{Number: 0}, nil })()
	defer swap(&loopFind, func(string) ([]string, error) { return []string{"/dev/loop0"}, nil })()

	defer swap(&dmAvailable, func() bool { return true })()
	defer swap(&dmVersion, func() (dm.DMVersion, error) { return dm.DMVersion{Major: 4}, nil })()
	defer swap(&dmCreate, func(string, string) error { return nil })()
	defer swap(&dmRemove, func(string) error { return nil })()
	defer swap(&dmSuspend, func(string) error { return nil })()
	defer swap(&dmResume, func(string) error { return nil })()
	defer swap(&dmInfo, func(string) (dm.DevInfo, error) { return dm.DevInfo{Name: "d"}, nil })()
	defer swap(&dmList, func() ([]dm.Device, error) { return []dm.Device{{Name: "d"}}, nil })()
	defer swap(&dmStatus, func(string) ([]dm.Target, error) { return []dm.Target{{Type: "x"}}, nil })()
	defer swap(&dmTableStatus, func(string) ([]dm.Target, error) { return []dm.Target{{Type: "x"}}, nil })()
	defer swap(&dmMessage, func(string, uint64, string) (string, error) { return "ok", nil })()
	defer swap(&dmLinear, func(a, b uint64, d string, o uint64) dm.Target { return dm.Target{Type: "linear"} })()
	defer swap(&dmCreateWithTable, func(string, []dm.Target) error { return nil })()

	defer swap(&btrfsAvailable, func(string) bool { return true })()
	defer swap(&btrfsSubvolCreate, func(string, string) error { return nil })()
	defer swap(&btrfsSubvolDelete, func(string, string) error { return nil })()
	defer swap(&btrfsSnapshotCreate, func(string, string, string, bool) error { return nil })()
	defer swap(&btrfsListSubvolumes, func(string) ([]btrfs.Subvolume, error) { return nil, nil })()
	defer swap(&btrfsGetSubvolInfo, func(string) (*btrfs.SubvolInfo, error) { return &btrfs.SubvolInfo{}, nil })()
	defer swap(&btrfsSubvolID, func(string) (uint64, error) { return 5, nil })()
	defer swap(&btrfsIsReadonly, func(string) (bool, error) { return false, nil })()
	defer swap(&btrfsSync, func(string) error { return nil })()

	defer swap(&zfsAvailable, func() bool { return true })()
	defer useHandle(&fakeHandle{poolNames: []string{"tank"}, rollbackTo: "tank@s", holds: map[string]uint64{}})()

	ok := []struct {
		method string
		args   []any
	}{
		{"loop_available", nil},
		{"LOOP_ATTACH", []any{"/img"}},
		{"loop_attach", []any{"/img", map[string]any{"read_only": true}}},
		{"loop_detach", []any{"/dev/loop0"}},
		{"loop_set_capacity", []any{"/dev/loop0"}},
		{"loop_status", []any{"/dev/loop0"}},
		{"loop_find", []any{"/img"}},
		{"dm_available", nil},
		{"dm_version", nil},
		{"dm_create", []any{"d"}},
		{"dm_create", []any{"d", "uuid"}},
		{"dm_remove", []any{"d"}},
		{"dm_suspend", []any{"d"}},
		{"dm_resume", []any{"d"}},
		{"dm_info", []any{"d"}},
		{"dm_list", nil},
		{"dm_status", []any{"d"}},
		{"dm_table", []any{"d"}},
		{"dm_message", []any{"d", int64(0), "ping"}},
		{"dm_create_linear", []any{"d", int64(0), int64(2048), "/dev/sda", int64(0)}},
		{"btrfs_available", []any{"/mnt"}},
		{"btrfs_subvolume_create", []any{"/mnt", "sv"}},
		{"btrfs_subvolume_delete", []any{"/mnt", "sv"}},
		{"btrfs_snapshot_create", []any{"/mnt/sv", "/mnt", "snap"}},
		{"btrfs_snapshot_create", []any{"/mnt/sv", "/mnt", "snap", true}},
		{"btrfs_subvolume_list", []any{"/mnt"}},
		{"btrfs_subvolume_info", []any{"/mnt/sv"}},
		{"btrfs_subvolume_id", []any{"/mnt/sv"}},
		{"btrfs_is_readonly", []any{"/mnt/sv"}},
		{"btrfs_sync", []any{"/mnt"}},
		{"zfs_available", nil},
		{"zfs_pool_names", nil},
		{"zfs_create_filesystem", []any{"tank/fs"}},
		{"zfs_destroy", []any{"tank/fs"}},
		{"zfs_destroy", []any{"tank/fs", true}},
		{"zfs_snapshot", []any{"tank", "tank/fs@s"}},
		{"zfs_rename", []any{"tank/a", "tank/b"}},
		{"zfs_rename", []any{"tank/a", "tank/b", true}},
		{"zfs_clone", []any{"tank/fs@s", "tank/c"}},
		{"zfs_rollback", []any{"tank/fs"}},
		{"zfs_holds", []any{"tank/fs@s"}},
	}
	for _, c := range ok {
		if _, err := s.Call(ctx, c.method, c.args...); err != nil {
			t.Errorf("Call(%q, %v) err = %v", c.method, c.args, err)
		}
	}
}

// TestCallArgErrors exercises every missing-argument branch and the unknown
// method branch.
func TestCallArgErrors(t *testing.T) {
	s := NewSession()
	ctx := context.Background()
	missing := [][]any{
		{"loop_attach"},
		{"loop_detach"},
		{"loop_set_capacity"},
		{"loop_status"},
		{"loop_find"},
		{"dm_create"},
		{"dm_remove"},
		{"dm_suspend"},
		{"dm_resume"},
		{"dm_info"},
		{"dm_status"},
		{"dm_table"},
		{"dm_message", "d", int64(0)},
		{"dm_create_linear", "d"},
		{"btrfs_available"},
		{"btrfs_subvolume_create", "/mnt"},
		{"btrfs_subvolume_delete", "/mnt"},
		{"btrfs_snapshot_create", "/mnt/sv", "/mnt"},
		{"btrfs_subvolume_list"},
		{"btrfs_subvolume_info"},
		{"btrfs_subvolume_id"},
		{"btrfs_is_readonly"},
		{"btrfs_sync"},
		{"zfs_create_filesystem"},
		{"zfs_destroy"},
		{"zfs_snapshot", "tank"},
		{"zfs_rename", "tank/a"},
		{"zfs_clone", "tank/fs@s"},
		{"zfs_rollback"},
		{"zfs_holds"},
		{"no_such_method"},
	}
	for _, m := range missing {
		method := m[0].(string)
		if _, err := s.Call(ctx, method, m[1:]...); err == nil {
			t.Errorf("Call(%q) expected error", method)
		}
	}
}

// ---------------------------------------------------------------------------
// coercion helpers + JSON error branches
// ---------------------------------------------------------------------------

func TestCoercion(t *testing.T) {
	if toString("x") != "x" || toString(nil) != "" || toString(42) != "42" || toString(stringer{}) != "S" {
		t.Error("toString")
	}
	if toUint64(uint64(1)) != 1 || toUint64(2) != 2 || toUint64(int64(3)) != 3 || toUint64(4.0) != 4 || toUint64("x") != 0 {
		t.Error("toUint64")
	}
	if toBool(true) != true || toBool(false) != false || toBool(nil) != false || toBool("x") != false {
		t.Error("toBool")
	}
	if argString(nil, 0, "def") != "def" || argString([]any{""}, 0, "def") != "def" || argString([]any{"a"}, 0, "def") != "a" {
		t.Error("argString")
	}
	if argBool(nil, 0, true) != true || argBool([]any{false}, 0, true) != false {
		t.Error("argBool")
	}
	if argHash(nil, 0) != nil || argHash([]any{"notahash"}, 0) != nil {
		t.Error("argHash non-map")
	}
	if h := argHash([]any{map[string]any{"k": 1}}, 0); h["k"] != 1 {
		t.Error("argHash map")
	}
	if hashUint64(nil, "k") != 0 || hashBool(nil, "k") != false {
		t.Error("hash* nil")
	}
	if !reflect.DeepEqual(toStrings([]any{"a", 2}), []string{"a", "2"}) {
		t.Error("toStrings")
	}
	if got := toStringArray([]string{"a", "b"}); len(got) != 2 || got[1] != "b" {
		t.Error("toStringArray")
	}
}

type stringer struct{}

func (stringer) String() string { return "S" }

func TestJSONErrorBranches(t *testing.T) {
	if _, err := toHash(make(chan int)); err == nil {
		t.Error("toHash should error on unmarshalable input")
	}
	if _, err := toArray(make(chan int)); err == nil {
		t.Error("toArray should error on unmarshalable input")
	}
	if _, err := toHash(42); err == nil {
		t.Error("toHash(scalar) should error")
	}
	if _, err := toArray(42); err == nil {
		t.Error("toArray(scalar) should error")
	}
}
