# frozen_string_literal: true
#
# SPDX-License-Identifier: BSD-3-Clause
# Copyright (c) 2026, the go-ruby-fsctl/fsctl authors
#
# Intended Ruby usage of the `fsctl` gem, whose pure-Go core is
# github.com/go-ruby-fsctl/fsctl. This file is documentation only: it is not
# executed here. The rbgo binding (owned by go-embedded-ruby) wires `require
# "fsctl"` to a Session and forwards snake_case method calls to Session#Call.
#
# All operations wrap the go-fsctl family of Linux kernel-ioctl helpers
# (loop / dm / btrfs / zfs) and require CAP_SYS_ADMIN (root) on a live Linux
# kernel. Off Linux — or without privilege — they raise the underlying error.

require "fsctl"

fs = Fsctl.new

# --- loop devices ----------------------------------------------------------
# Attach a backing file to the first free /dev/loopN and get its path back.
dev = fs.loop_attach("/tmp/disk.img")            # => "/dev/loop3"
dev = fs.loop_attach("/tmp/disk.img",            # options Hash is optional
                     offset: 1024 * 1024,
                     read_only: true,
                     part_scan: true)

info = fs.loop_status(dev)                        # => { "Number" => 3, "BackingFile" => "/tmp/disk.img", ... }
fs.loop_find("/tmp/disk.img")                     # => ["/dev/loop3"]
fs.loop_set_capacity(dev)                         # re-read backing file size after growing it
fs.loop_detach(dev)

# --- device-mapper ---------------------------------------------------------
fs.dm_version                                     # => { "Major" => 4, "Minor" => 48, "Patch" => 0 }
fs.dm_create("myvol")                             # empty device, optional uuid: dm_create("myvol", "uuid")
# One "linear" target: 2048 sectors of /dev/sda starting at its sector 0.
fs.dm_create_linear("myvol", 0, 2048, "/dev/sda", 0)
fs.dm_info("myvol")                               # => Hash (name, uuid, open_count, ...)
fs.dm_table("myvol")                              # => Array of target Hashes
fs.dm_status("myvol")                             # => Array of target-status Hashes
fs.dm_message("myvol", 0, "some target message")  # => reply String
fs.dm_list.each { |d| puts d["Name"] }            # => all mapped devices
fs.dm_suspend("myvol"); fs.dm_resume("myvol")
fs.dm_remove("myvol")

# --- btrfs -----------------------------------------------------------------
fs.btrfs_available("/mnt")                         # => true on a btrfs mount
fs.btrfs_subvolume_create("/mnt", "data")
fs.btrfs_snapshot_create("/mnt/data", "/mnt", "data-ro", true)   # read-only snapshot
fs.btrfs_subvolume_list("/mnt").each { |s| puts s["Name"] }
fs.btrfs_subvolume_info("/mnt/data")               # => Hash (id, parent_id, generation, ...)
fs.btrfs_subvolume_id("/mnt/data")                 # => Integer
fs.btrfs_is_readonly("/mnt/data-ro")               # => true
fs.btrfs_sync("/mnt")
fs.btrfs_subvolume_delete("/mnt", "data")

# --- zfs -------------------------------------------------------------------
fs.zfs_available                                   # => true when /dev/zfs is present
fs.zfs_pool_names                                  # => ["tank", "rpool"]
fs.zfs_create_filesystem("tank/projects")
fs.zfs_snapshot("tank", "tank/projects@backup")    # extra args add more snapshots
fs.zfs_clone("tank/projects@backup", "tank/restore")
fs.zfs_holds("tank/projects@backup")               # => { "keep" => 1700000000 }
fs.zfs_rename("tank/projects", "tank/work")
fs.zfs_rollback("tank/work")                        # => rolled-back snapshot name
fs.zfs_destroy("tank/restore")                      # dm_destroy("tank/restore", true) to defer
