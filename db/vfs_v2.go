package db

import (
	"time"

	"github.com/cockroachdb/pebble/v2/vfs"
	"github.com/rjl493456442/pebble-bench/metrics"
)

// instrumentV2FS wraps a Pebble v2 vfs.FS so that the durability and read
// syscalls on files it produces are counted and timed into the trackers. If
// base is nil, vfs.Default is used. When both trackers are nil, base is
// returned unchanged.
func instrumentV2FS(base vfs.FS, syncTracker *metrics.SyncTracker, readTracker *metrics.ReadTracker) vfs.FS {
	if syncTracker == nil && readTracker == nil {
		return base
	}
	if base == nil {
		base = vfs.Default
	}
	return &v2SyncFS{FS: base, syncTracker: syncTracker, readTracker: readTracker}
}

// v2SyncFS embeds the underlying FS so that non-file-returning methods are
// forwarded unchanged; only the methods that hand out files are overridden to
// wrap their result. The v2 FS threads a DiskWriteCategory through its
// file-opening methods, which we pass along untouched.
type v2SyncFS struct {
	vfs.FS
	syncTracker *metrics.SyncTracker
	readTracker *metrics.ReadTracker
}

var _ vfs.FS = (*v2SyncFS)(nil)

func (fs *v2SyncFS) wrap(f vfs.File) vfs.File {
	return &v2SyncFile{File: f, syncTracker: fs.syncTracker, readTracker: fs.readTracker}
}

func (fs *v2SyncFS) Create(name string, category vfs.DiskWriteCategory) (vfs.File, error) {
	f, err := fs.FS.Create(name, category)
	if err != nil {
		return nil, err
	}
	return fs.wrap(f), nil
}

func (fs *v2SyncFS) Open(name string, opts ...vfs.OpenOption) (vfs.File, error) {
	f, err := fs.FS.Open(name, opts...)
	if err != nil {
		return nil, err
	}
	return fs.wrap(f), nil
}

func (fs *v2SyncFS) OpenReadWrite(name string, category vfs.DiskWriteCategory, opts ...vfs.OpenOption) (vfs.File, error) {
	f, err := fs.FS.OpenReadWrite(name, category, opts...)
	if err != nil {
		return nil, err
	}
	return fs.wrap(f), nil
}

func (fs *v2SyncFS) OpenDir(name string) (vfs.File, error) {
	f, err := fs.FS.OpenDir(name)
	if err != nil {
		return nil, err
	}
	return fs.wrap(f), nil
}

func (fs *v2SyncFS) ReuseForWrite(oldname, newname string, category vfs.DiskWriteCategory) (vfs.File, error) {
	f, err := fs.FS.ReuseForWrite(oldname, newname, category)
	if err != nil {
		return nil, err
	}
	return fs.wrap(f), nil
}

// v2SyncFile embeds the underlying File so that writes, Fd, Preallocate, etc.
// forward unchanged; only the durability and read operations are timed. Either
// tracker may be nil, in which case that category is not instrumented.
type v2SyncFile struct {
	vfs.File
	syncTracker *metrics.SyncTracker
	readTracker *metrics.ReadTracker
}

var _ vfs.File = (*v2SyncFile)(nil)

func (f *v2SyncFile) Sync() error {
	if f.syncTracker == nil {
		return f.File.Sync()
	}
	start := time.Now()
	err := f.File.Sync()
	f.syncTracker.Record(metrics.OpSync, time.Since(start))
	return err
}

func (f *v2SyncFile) SyncData() error {
	if f.syncTracker == nil {
		return f.File.SyncData()
	}
	start := time.Now()
	err := f.File.SyncData()
	f.syncTracker.Record(metrics.OpSyncData, time.Since(start))
	return err
}

func (f *v2SyncFile) SyncTo(length int64) (fullSync bool, err error) {
	if f.syncTracker == nil {
		return f.File.SyncTo(length)
	}
	start := time.Now()
	fullSync, err = f.File.SyncTo(length)
	f.syncTracker.Record(metrics.OpSyncTo, time.Since(start))
	return fullSync, err
}

func (f *v2SyncFile) Read(p []byte) (int, error) {
	if f.readTracker == nil {
		return f.File.Read(p)
	}
	start := time.Now()
	n, err := f.File.Read(p)
	f.readTracker.Record(metrics.OpRead, time.Since(start))
	return n, err
}

func (f *v2SyncFile) ReadAt(p []byte, off int64) (int, error) {
	if f.readTracker == nil {
		return f.File.ReadAt(p, off)
	}
	start := time.Now()
	n, err := f.File.ReadAt(p, off)
	f.readTracker.Record(metrics.OpReadAt, time.Since(start))
	return n, err
}
