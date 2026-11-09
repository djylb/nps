package install

import (
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/djylb/selfupdate"
)

// keepUserFiles gives the directories of an update overwrite semantics. The
// update installs a directory as a whole tree, which would delete what the
// user keeps in it, such as a custom error page. For each directory of files,
// paths holds the staged new tree in the same position; keepUserFiles copies
// into it every file and symlink of the installed tree that the release does
// not have, so after the swap the files of the release are added or replaced
// and all others stay. The installed tree is only read, and the staged one is
// swapped in with the rest of the set by the journal of the update, so a
// failure here or later leaves the installation as it was and a crash is
// undone on the next start.
//
// A relative Target is relative to base, the directory of the executable. Where
// the release and the installation differ in kind (file against directory),
// the release wins.
func keepUserFiles(files []selfupdate.File, paths []string, base string) error {
	for i, f := range files {
		if !f.Dir || i >= len(paths) {
			continue
		}
		old := f.Target
		if !filepath.IsAbs(old) {
			old = filepath.Join(base, old)
		}
		if err := mergeTree(old, paths[i]); err != nil {
			return fmt.Errorf("keeping the files in %s: %w", old, err)
		}
	}
	return nil
}

// mergeTree copies what is in old and not in staged into staged. A missing old
// tree is nothing to keep.
func mergeTree(old, staged string) error {
	if fi, err := os.Lstat(old); err != nil || !fi.IsDir() {
		if err == nil || os.IsNotExist(err) {
			return nil
		}
		return err
	}
	return filepath.WalkDir(old, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(old, p)
		if err != nil {
			return err
		}
		if rel == "." {
			return nil
		}
		dst := filepath.Join(staged, rel)
		have, err := os.Lstat(dst)
		switch {
		case err == nil:
			if have.IsDir() && d.IsDir() {
				return nil
			}
			if d.IsDir() {
				return filepath.SkipDir // the release has a file here
			}
			return nil // the release's file wins
		case !os.IsNotExist(err):
			return err
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		switch {
		case d.IsDir():
			return os.Mkdir(dst, info.Mode().Perm()|0o700) // writable while filled
		case info.Mode()&fs.ModeSymlink != 0:
			link, err := os.Readlink(p)
			if err != nil {
				return err
			}
			return os.Symlink(link, dst)
		case info.Mode().IsRegular():
			return copyKept(p, dst, info.Mode().Perm())
		}
		return nil // devices, sockets and the like are not kept
	})
}

func copyKept(src, dst string, perm fs.FileMode) (err error) {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer func() { _ = in.Close() }()
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_EXCL, perm)
	if err != nil {
		return err
	}
	defer func() {
		if cerr := out.Close(); err == nil {
			err = cerr
		}
	}()
	if _, err = io.Copy(out, in); err != nil {
		return err
	}
	if err = out.Sync(); err != nil {
		return err
	}
	return os.Chmod(dst, perm) // not subject to the umask
}
