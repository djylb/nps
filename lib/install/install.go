package install

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/djylb/nps/lib/runenv"
	"github.com/djylb/nps/lib/version"
	"github.com/djylb/selfupdate"
)

// copyServerAssets copies the web UI and the reference config from srcDir to
// destDir. Each item is handled on its own: an item missing from srcDir keeps
// the copy already in destDir and is an error only when destDir has none.
func copyServerAssets(srcDir, destDir string) error {
	items := []struct {
		src, dest string
		dir       bool
	}{
		{src: "web/views", dest: "web/views", dir: true},
		{src: "web/static", dest: "web/static", dir: true},
		{src: "conf/nps.conf", dest: "conf/nps.conf.default"},
	}
	for _, it := range items {
		src := filepath.Join(srcDir, filepath.FromSlash(it.src))
		dest := filepath.Join(destDir, filepath.FromSlash(it.dest))
		if ok, err := pathExists(src); err != nil {
			return err
		} else if !ok {
			if exists, _ := pathExists(dest); exists {
				fmt.Printf("%s not found in %s, keep %s\n", it.src, srcDir, dest)
				continue
			}
			return fmt.Errorf("%s not found in %s", it.src, srcDir)
		}
		var err error
		if it.dir {
			err = CopyDir(src, dest)
		} else {
			_, err = copyFile(src, dest)
		}
		if err != nil {
			return err
		}
		chMod(dest, 0766)
	}
	return nil
}

// installFile replaces target with a copy of the executable src through the
// update journal of target, so that a running target is never truncated and
// an interrupted install is undone on the next start. Installing a file
// over itself does nothing.
func installFile(src, target string) error {
	if same, _ := sameFile(src, target); same {
		return nil
	}
	ctx := context.Background()
	staged, err := selfupdate.JournalFor(target).Stage(ctx, target)
	if err != nil {
		return err
	}
	defer func() { _ = staged.Abort(ctx) }()
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer func() { _ = in.Close() }()
	out, err := os.OpenFile(staged.Paths()[0], os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o755)
	if err != nil {
		return err
	}
	_, err = io.Copy(out, in)
	if err == nil {
		err = out.Sync()
	}
	if cerr := out.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return err
	}
	return staged.Replace(ctx, selfupdate.ReplaceOptions{Version: version.VERSION})
}

func currentExecutable() (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", err
	}
	return filepath.EvalSymlinks(exe)
}

func containsFile(paths []string, p string) bool {
	fi, err := os.Stat(p)
	if err != nil {
		return false
	}
	for _, q := range paths {
		if qi, err := os.Stat(q); err == nil && os.SameFile(fi, qi) {
			return true
		}
	}
	return false
}

// NPC installs the npc executable and returns the path the service should run.
func NPC() (string, error) {
	if err := os.MkdirAll(runenv.GetInstallPath(), 0o755); err != nil {
		return "", err
	}
	return installExecutable(runenv.GetAppPath(), "npc")
}

// NPS installs the nps executable, web assets and, on a fresh install, the
// config directory. It returns the path the service should run.
func NPS() (string, error) {
	path := runenv.GetInstallPath()
	appPath := runenv.GetAppPath()
	fmt.Println("install path:", path)
	if exists, err := pathExists(path); err != nil {
		return "", err
	} else if !exists {
		// keep the existing config when reinstalling
		if err := CopyDir(filepath.Join(appPath, "conf"), filepath.Join(path, "conf")); err != nil {
			return "", err
		}
		chMod(filepath.Join(path, "conf"), 0766)
	}
	if err := copyServerAssets(appPath, path); err != nil {
		return "", err
	}
	binPath, err := installExecutable(appPath, "nps")
	if err != nil {
		return "", err
	}
	fmt.Println("install ok!")
	fmt.Println("Static files and configuration files in the current directory will be useless")
	fmt.Println("The new configuration file is located in", path, "you can edit them")
	fmt.Println("You can start with:\n" + npsUsage)
	chMod(runenv.GetLogPath(), 0777)
	return binPath, nil
}

// CopyDir copies the files under srcPath into destPath, creating directories
// as needed.
func CopyDir(srcPath string, destPath string) error {
	srcInfo, err := os.Stat(srcPath)
	if err != nil {
		return err
	}
	if !srcInfo.IsDir() {
		return errors.New("srcPath is not a directory")
	}
	if destInfo, err := os.Stat(destPath); err == nil {
		if !destInfo.IsDir() {
			return errors.New("destPath is not a directory")
		}
	} else if !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	return filepath.WalkDir(srcPath, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(srcPath, path)
		if err != nil {
			return err
		}
		target := filepath.Join(destPath, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		fmt.Println("copy file:", path, "->", target)
		if _, err := copyFile(path, target); err != nil {
			return err
		}
		chMod(target, 0766)
		return nil
	})
}

// copyFile copies src to dest, creating the parent directory. Copying a file
// onto itself is a no-op.
func copyFile(src, dest string) (int64, error) {
	srcInfo, err := os.Stat(src)
	if err != nil {
		return 0, err
	}
	if destInfo, err := os.Stat(dest); err == nil && os.SameFile(srcInfo, destInfo) {
		return 0, nil
	}
	in, err := os.Open(src)
	if err != nil {
		return 0, err
	}
	defer func() { _ = in.Close() }()
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		return 0, err
	}
	out, err := os.Create(dest)
	if err != nil {
		return 0, err
	}
	n, err := io.Copy(out, in)
	if cerr := out.Close(); err == nil {
		err = cerr
	}
	return n, err
}

// 检测文件夹路径是否存在
func pathExists(path string) (bool, error) {
	_, err := os.Stat(path)
	if err == nil {
		return true, nil
	}
	if os.IsNotExist(err) {
		return false, nil
	}
	return false, err
}
