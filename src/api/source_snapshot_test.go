package api

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

const (
	sourceSnapshotCommitDate  = "2000-01-01T00:00:00Z"
	sourceSnapshotCommitEmail = "source-snapshot@example.invalid"
	sourceSnapshotCommitName  = "Process Compose Source Snapshot"
)

func TestCreateSourceSnapshotRepresentsUnstagedTrackedDeletion(t *testing.T) {
	repositoryRoot := filepath.Join(t.TempDir(), "repository")
	if err := os.Mkdir(repositoryRoot, 0o755); err != nil {
		t.Fatalf("create test repository: %v", err)
	}
	runSourceSnapshotGit(t, repositoryRoot, nil, "init", "--quiet", "--object-format=sha1")
	runSourceSnapshotGit(t, repositoryRoot, nil, "config", "--local", "user.name", sourceSnapshotCommitName)
	runSourceSnapshotGit(t, repositoryRoot, nil, "config", "--local", "user.email", sourceSnapshotCommitEmail)

	for name, contents := range map[string]string{
		"deleted.txt": "delete this working-tree file\n",
		"kept.txt":    "keep this tracked file\n",
	} {
		if err := os.WriteFile(filepath.Join(repositoryRoot, name), []byte(contents), 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	runSourceSnapshotGit(t, repositoryRoot, nil, "add", "--all", "--", ".")
	runSourceSnapshotGit(
		t,
		repositoryRoot,
		[]string{
			"GIT_AUTHOR_DATE=" + sourceSnapshotCommitDate,
			"GIT_COMMITTER_DATE=" + sourceSnapshotCommitDate,
		},
		"commit", "--quiet", "--no-gpg-sign", "--no-verify", "--message", "test repository base",
	)

	if err := os.Remove(filepath.Join(repositoryRoot, "deleted.txt")); err != nil {
		t.Fatalf("delete tracked working-tree file: %v", err)
	}
	if err := os.WriteFile(filepath.Join(repositoryRoot, "untracked.txt"), []byte("include current untracked content\n"), 0o644); err != nil {
		t.Fatalf("write untracked working-tree file: %v", err)
	}

	snapshotRoot, snapshotRevision := createSourceSnapshot(t, repositoryRoot)
	tracked := runSourceSnapshotGit(t, snapshotRoot, nil, "ls-tree", "--name-only", "-r", "HEAD")
	if got, want := string(tracked), "kept.txt\nuntracked.txt\n"; got != want {
		t.Fatalf("snapshot tree entries = %q, want %q", got, want)
	}
	_, repeatedRevision := createSourceSnapshot(t, repositoryRoot)
	if repeatedRevision != snapshotRevision {
		t.Fatalf("repeated snapshot revision = %q, want deterministic revision %q", repeatedRevision, snapshotRevision)
	}
}

func createSourceSnapshot(t *testing.T, repositoryRoot string) (snapshotRoot, snapshotRevision string) {
	t.Helper()

	absoluteRepositoryRoot, err := filepath.Abs(repositoryRoot)
	if err != nil {
		t.Fatalf("resolve repository root %q: %v", repositoryRoot, err)
	}
	resolvedRepositoryRoot, err := filepath.EvalSymlinks(absoluteRepositoryRoot)
	if err != nil {
		t.Fatalf("resolve repository root symlinks %q: %v", absoluteRepositoryRoot, err)
	}

	listed := runSourceSnapshotGit(
		t,
		absoluteRepositoryRoot,
		nil,
		"ls-files", "--cached", "--others", "--exclude-standard", "-z", "--",
	)
	relativePaths := parseSourceSnapshotPaths(t, listed)
	deleted := parseSourceSnapshotPaths(t, runSourceSnapshotGit(
		t,
		absoluteRepositoryRoot,
		nil,
		"ls-files", "--deleted", "-z", "--",
	))
	if len(deleted) > 0 {
		deletedPaths := make(map[string]struct{}, len(deleted))
		for _, relativePath := range deleted {
			deletedPaths[relativePath] = struct{}{}
		}
		presentPaths := relativePaths[:0]
		for _, relativePath := range relativePaths {
			if _, isDeleted := deletedPaths[relativePath]; !isDeleted {
				presentPaths = append(presentPaths, relativePath)
			}
		}
		relativePaths = presentPaths
	}

	snapshotRoot = filepath.Join(t.TempDir(), "source")
	if err := os.Mkdir(snapshotRoot, 0o755); err != nil {
		t.Fatalf("create source snapshot root: %v", err)
	}

	// Create every parent directory before copying entries. This prevents a
	// malicious symlink entry from redirecting a later file outside the snapshot.
	for _, relativePath := range relativePaths {
		destinationPath := filepath.Join(snapshotRoot, relativePath)
		if err := requirePathWithinRoot(snapshotRoot, destinationPath); err != nil {
			t.Fatalf("reject snapshot destination %q: %v", relativePath, err)
		}
		if err := os.MkdirAll(filepath.Dir(destinationPath), 0o755); err != nil {
			t.Fatalf("create parent directory for %q: %v", relativePath, err)
		}
	}

	for _, relativePath := range relativePaths {
		sourcePath := filepath.Join(absoluteRepositoryRoot, relativePath)
		resolvedSourceParent, err := filepath.EvalSymlinks(filepath.Dir(sourcePath))
		if err != nil {
			t.Fatalf("resolve source parent for %q: %v", relativePath, err)
		}
		if err := requirePathWithinRoot(resolvedRepositoryRoot, resolvedSourceParent); err != nil {
			t.Fatalf("reject source path %q: %v", relativePath, err)
		}
		copySourceSnapshotEntry(t, sourcePath, filepath.Join(snapshotRoot, relativePath), relativePath)
	}

	runSourceSnapshotGit(t, snapshotRoot, nil, "init", "--quiet", "--object-format=sha1")
	runSourceSnapshotGit(t, snapshotRoot, nil, "config", "--local", "user.name", sourceSnapshotCommitName)
	runSourceSnapshotGit(t, snapshotRoot, nil, "config", "--local", "user.email", sourceSnapshotCommitEmail)
	runSourceSnapshotGit(t, snapshotRoot, nil, "add", "--all", "--force", "--", ".")
	commitEnvironment := []string{
		"GIT_AUTHOR_DATE=" + sourceSnapshotCommitDate,
		"GIT_COMMITTER_DATE=" + sourceSnapshotCommitDate,
	}
	runSourceSnapshotGit(
		t,
		snapshotRoot,
		commitEnvironment,
		"commit", "--quiet", "--no-gpg-sign", "--no-verify", "--message", "Process Compose source snapshot",
	)

	revision := runSourceSnapshotGit(t, snapshotRoot, nil, "rev-parse", "HEAD")
	snapshotRevision = strings.TrimSpace(string(revision))
	if snapshotRevision == "" {
		t.Fatal("source snapshot Git revision is empty")
	}
	return snapshotRoot, snapshotRevision
}

func parseSourceSnapshotPaths(t *testing.T, listed []byte) []string {
	t.Helper()
	if len(listed) == 0 {
		return nil
	}
	if listed[len(listed)-1] != 0 {
		t.Fatal("git ls-files output is not NUL-terminated")
	}

	entries := bytes.Split(listed[:len(listed)-1], []byte{0})
	paths := make([]string, 0, len(entries))
	seen := make(map[string]struct{}, len(entries))
	for _, entry := range entries {
		gitPath := string(entry)
		if gitPath == "" {
			t.Fatal("git ls-files returned an empty path")
		}
		relativePath := filepath.FromSlash(gitPath)
		cleanPath := filepath.Clean(relativePath)
		if cleanPath != relativePath || cleanPath == "." || filepath.IsAbs(cleanPath) || filepath.VolumeName(cleanPath) != "" {
			t.Fatalf("git ls-files returned a non-canonical relative path %q", gitPath)
		}
		if cleanPath == ".." || strings.HasPrefix(cleanPath, ".."+string(os.PathSeparator)) {
			t.Fatalf("git ls-files path escapes the repository root: %q", gitPath)
		}
		for _, component := range strings.Split(filepath.ToSlash(cleanPath), "/") {
			if strings.EqualFold(component, ".git") {
				t.Fatalf("git ls-files path collides with snapshot metadata: %q", gitPath)
			}
		}
		if _, duplicate := seen[cleanPath]; duplicate {
			t.Fatalf("git ls-files returned duplicate path %q", gitPath)
		}
		seen[cleanPath] = struct{}{}
		paths = append(paths, cleanPath)
	}
	return paths
}

func copySourceSnapshotEntry(t *testing.T, sourcePath, destinationPath, relativePath string) {
	t.Helper()
	info, err := os.Lstat(sourcePath)
	if err != nil {
		t.Fatalf("inspect source snapshot entry %q: %v", relativePath, err)
	}

	switch {
	case info.Mode().IsRegular():
		copySourceSnapshotFile(t, sourcePath, destinationPath, relativePath, info.Mode())
	case info.Mode()&os.ModeSymlink != 0:
		target, err := os.Readlink(sourcePath)
		if err != nil {
			t.Fatalf("read source snapshot symlink %q: %v", relativePath, err)
		}
		if err := os.Symlink(target, destinationPath); err != nil {
			t.Fatalf("create source snapshot symlink %q: %v", relativePath, err)
		}
	default:
		t.Fatalf("source snapshot entry %q has unsupported mode %s", relativePath, info.Mode())
	}
}

func copySourceSnapshotFile(t *testing.T, sourcePath, destinationPath, relativePath string, mode os.FileMode) {
	t.Helper()
	source, err := os.Open(sourcePath)
	if err != nil {
		t.Fatalf("open source snapshot entry %q: %v", relativePath, err)
	}
	destination, err := os.OpenFile(destinationPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, mode.Perm())
	if err != nil {
		_ = source.Close()
		t.Fatalf("create source snapshot entry %q: %v", relativePath, err)
	}

	_, copyErr := io.Copy(destination, source)
	destinationCloseErr := destination.Close()
	sourceCloseErr := source.Close()
	if copyErr != nil {
		t.Fatalf("copy source snapshot entry %q: %v", relativePath, copyErr)
	}
	if destinationCloseErr != nil {
		t.Fatalf("close copied source snapshot entry %q: %v", relativePath, destinationCloseErr)
	}
	if sourceCloseErr != nil {
		t.Fatalf("close source snapshot entry %q: %v", relativePath, sourceCloseErr)
	}
	if err := os.Chmod(destinationPath, mode.Perm()); err != nil {
		t.Fatalf("preserve source snapshot mode for %q: %v", relativePath, err)
	}
}

func requirePathWithinRoot(root, candidate string) error {
	relativePath, err := filepath.Rel(root, candidate)
	if err != nil {
		return err
	}
	if filepath.IsAbs(relativePath) || relativePath == ".." || strings.HasPrefix(relativePath, ".."+string(os.PathSeparator)) {
		return fmt.Errorf("%q escapes %q", candidate, root)
	}
	return nil
}

func runSourceSnapshotGit(t *testing.T, directory string, extraEnvironment []string, arguments ...string) []byte {
	t.Helper()
	command := exec.Command("git", arguments...)
	command.Dir = directory
	command.Env = sourceSnapshotEnvironment(extraEnvironment)
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s in %q: %v\n%s", strings.Join(arguments, " "), directory, err, output)
	}
	return output
}

func sourceSnapshotEnvironment(extra []string) []string {
	environment := make([]string, 0, len(os.Environ())+len(extra)+4)
	for _, variable := range os.Environ() {
		name, _, _ := strings.Cut(variable, "=")
		upperName := strings.ToUpper(name)
		if strings.HasPrefix(upperName, "GIT_") || upperName == "LC_ALL" || upperName == "TZ" {
			continue
		}
		environment = append(environment, variable)
	}
	environment = append(environment,
		"GIT_CONFIG_NOSYSTEM=1",
		"GIT_CONFIG_GLOBAL="+os.DevNull,
		"LC_ALL=C",
		"TZ=UTC",
	)
	environment = append(environment, extra...)
	return environment
}
