package passocli

import (
	_ "embed"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"
)

const (
	passoSkillDirectory = "passo"
	passoSkillMarker    = ".managed-by-passo-cli"
	passoSkillOwner     = "managed-by-passo-cli:pyxcloud-passo-skill:v1\n"
)

//go:embed assets/passo/SKILL.md
var embeddedPassoSkill []byte

func newAgentSetupCommand(homeDir func() (string, error)) *cobra.Command {
	setup := &cobra.Command{Use: "setup", Short: "Install managed agent workflow guidance", Args: cobra.NoArgs}
	for _, name := range []string{"codex", "agents"} {
		command := &cobra.Command{Use: name, Short: "Install the shared passo agent skill", Args: cobra.NoArgs}
		var dryRun, remove bool
		command.Flags().BoolVar(&dryRun, "dry-run", false, "show the planned action without changing files")
		command.Flags().BoolVar(&remove, "remove", false, "remove the managed passo skill")
		command.RunE = func(cmd *cobra.Command, _ []string) error {
			home, err := homeDir()
			if err != nil || strings.TrimSpace(home) == "" {
				return errors.New("could not resolve home directory")
			}
			target, action, err := planPassoSkill(home, remove)
			if err != nil {
				return err
			}
			if dryRun {
				_, err = fmt.Fprintf(cmd.OutOrStdout(), "dry-run: %s %s\n", action, target)
				return err
			}
			if remove {
				if action == "remove" {
					if err := removeManagedPassoSkill(target); err != nil {
						return err
					}
				}
				_, err = fmt.Fprintf(cmd.OutOrStdout(), "%s %s\n", action, target)
				return err
			}
			if err := installManagedPassoSkill(home, target, action); err != nil {
				return err
			}
			_, err = fmt.Fprintf(cmd.OutOrStdout(), "%s %s\n", action, target)
			return err
		}
		setup.AddCommand(command)
	}
	return setup
}

func planPassoSkill(home string, remove bool) (target, action string, err error) {
	home, err = filepath.Abs(home)
	if err != nil {
		return "", "", errors.New("invalid home directory")
	}
	target = filepath.Join(home, ".agents", "skills", passoSkillDirectory)
	if err := checkPassoSkillPath(home, target); err != nil {
		return target, "", err
	}
	info, err := os.Lstat(target)
	if errors.Is(err, os.ErrNotExist) {
		if remove {
			return target, "already absent", nil
		}
		return target, "install", nil
	}
	if err != nil {
		return target, "", errors.New("could not inspect skill directory")
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return target, "", errors.New("skill target is not a safe directory")
	}
	markerPath := filepath.Join(target, passoSkillMarker)
	markerInfo, err := os.Lstat(markerPath)
	if err != nil || !markerInfo.Mode().IsRegular() || markerInfo.Mode()&os.ModeSymlink != 0 {
		return target, "", errors.New("refusing unmanaged skill directory")
	}
	marker, err := os.ReadFile(markerPath)
	if err != nil || string(marker) != passoSkillOwner {
		return target, "", errors.New("refusing unmanaged skill directory")
	}
	if err := checkManagedSkillFile(filepath.Join(target, "SKILL.md"), true); err != nil {
		return target, "", err
	}
	if remove {
		entries, err := os.ReadDir(target)
		if err != nil {
			return target, "", errors.New("could not inspect managed skill files")
		}
		for _, entry := range entries {
			if entry.Name() != passoSkillMarker && entry.Name() != "SKILL.md" {
				return target, "", errors.New("refusing removal because managed skill directory contains unknown files")
			}
		}
		return target, "remove", nil
	}
	return target, "refresh", nil
}

func checkPassoSkillPath(home, target string) error {
	homeInfo, err := os.Lstat(home)
	if err != nil || !homeInfo.IsDir() || homeInfo.Mode()&os.ModeSymlink != 0 {
		return errors.New("home directory is not a safe directory")
	}
	rel, err := filepath.Rel(home, target)
	if err != nil || rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return errors.New("skill target escapes home directory")
	}
	current := home
	for _, part := range strings.Split(rel, string(filepath.Separator)) {
		current = filepath.Join(current, part)
		info, err := os.Lstat(current)
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		if err != nil || info.Mode()&os.ModeSymlink != 0 {
			return errors.New("refusing symlink or inaccessible component in skill path")
		}
		if current != target && !info.IsDir() {
			return errors.New("skill path parent is not a directory")
		}
	}
	return nil
}

func checkManagedSkillFile(path string, allowMissing bool) error {
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) && allowMissing {
		return nil
	}
	if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		return errors.New("managed skill file is not a regular file")
	}
	return nil
}

func installManagedPassoSkill(home, target, action string) error {
	if action == "install" {
		for _, dir := range []string{filepath.Join(home, ".agents"), filepath.Join(home, ".agents", "skills")} {
			if err := ensurePrivateDirectory(dir); err != nil {
				return err
			}
		}
		if err := os.Mkdir(target, 0700); err != nil {
			return errors.New("could not create managed skill directory")
		}
		if err := writePassoOwnerMarker(filepath.Join(target, passoSkillMarker)); err != nil {
			return err
		}
	}
	if err := checkPassoSkillPath(home, target); err != nil {
		return err
	}
	markerPath := filepath.Join(target, passoSkillMarker)
	marker, err := os.ReadFile(markerPath)
	if err != nil || string(marker) != passoSkillOwner {
		return errors.New("refusing to refresh unmanaged skill directory")
	}
	if err := checkManagedSkillFile(filepath.Join(target, "SKILL.md"), true); err != nil {
		return err
	}
	if err := os.Chmod(target, 0700); err != nil {
		return errors.New("could not secure managed skill directory")
	}
	return writePassoSkillAtomic(filepath.Join(target, "SKILL.md"), embeddedPassoSkill)
}

func ensurePrivateDirectory(path string) error {
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		if err := os.Mkdir(path, 0700); err == nil {
			return nil
		}
		info, err = os.Lstat(path)
	}
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return errors.New("refusing unsafe skill parent directory")
	}
	return nil
}

func writePassoOwnerMarker(path string) error {
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return errors.New("could not create managed skill ownership marker")
	}
	if _, err = io.WriteString(file, passoSkillOwner); err != nil {
		_ = file.Close()
		return errors.New("could not write managed skill ownership marker")
	}
	if err = file.Sync(); err != nil {
		_ = file.Close()
		return errors.New("could not sync managed skill ownership marker")
	}
	if err = file.Close(); err != nil {
		return errors.New("could not close managed skill ownership marker")
	}
	return nil
}

func writePassoSkillAtomic(path string, content []byte) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".passo-skill-*")
	if err != nil {
		return errors.New("could not create temporary managed skill file")
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)
	if err := tmp.Chmod(0600); err != nil {
		_ = tmp.Close()
		return errors.New("could not secure temporary managed skill file")
	}
	if _, err := tmp.Write(content); err != nil {
		_ = tmp.Close()
		return errors.New("could not write managed skill file")
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return errors.New("could not sync managed skill file")
	}
	if err := tmp.Close(); err != nil {
		return errors.New("could not close managed skill file")
	}
	if err := os.Rename(tmpPath, path); err != nil {
		return errors.New("could not atomically install managed skill file")
	}
	if err := syncPassoSkillDirectory(dir); err != nil {
		return errors.New("could not sync managed skill directory")
	}
	return nil
}

func syncPassoSkillDirectory(path string) error {
	dir, err := os.Open(path)
	if err != nil {
		return err
	}
	syncErr := dir.Sync()
	closeErr := dir.Close()
	return errors.Join(syncErr, closeErr)
}

func removeManagedPassoSkill(target string) error {
	home := filepath.Dir(filepath.Dir(filepath.Dir(target)))
	checkedTarget, action, err := planPassoSkill(home, true)
	if err != nil || checkedTarget != target || action != "remove" {
		return errors.New("refusing to remove unmanaged skill directory")
	}
	if err := os.Remove(filepath.Join(target, "SKILL.md")); err != nil && !errors.Is(err, os.ErrNotExist) {
		return errors.New("could not remove managed skill file")
	}
	if err := os.Remove(filepath.Join(target, passoSkillMarker)); err != nil {
		return errors.New("could not remove managed skill marker")
	}
	if err := os.Remove(target); err != nil {
		return errors.New("could not remove empty managed skill directory")
	}
	return nil
}
