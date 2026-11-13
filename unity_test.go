package samples

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// unityPackage is the Unity package folder. Unity reads only files with a .meta beside them in a package it cannot
// write to (one added by git URL or from a registry), so every file and folder it imports carries one in git.
// Unity skips names that start with a dot and folders that end in ~ (Tests~), and so does this test.
const unityPackage = "unity"

var unityGUID = regexp.MustCompile(`(?m)^guid: ([0-9a-f]{32})$`)

// TestUnityPackageMetaFiles fails when a file or folder in the Unity package has no .meta, when two share a GUID,
// or when a .meta is left with nothing beside it. UNITY_META=write writes the missing ones, each with a GUID fixed
// by its path, so writing them again gives the same files.
func TestUnityPackageMetaFiles(t *testing.T) {
	write := os.Getenv("UNITY_META") == "write"
	seen := map[string]string{}
	err := filepath.WalkDir(unityPackage, func(path string, d fs.DirEntry, err error) error {
		if err != nil || path == unityPackage {
			return err
		}
		name := d.Name()
		if strings.HasPrefix(name, ".") || strings.HasSuffix(name, "~") {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if strings.HasSuffix(name, ".meta") {
			if _, err := os.Stat(strings.TrimSuffix(path, ".meta")); err != nil {
				t.Errorf("%s: a .meta with no file or folder beside it", path)
			}
			return nil
		}
		meta, err := os.ReadFile(path + ".meta")
		if os.IsNotExist(err) && write {
			meta = unityMeta(path, d.IsDir())
			err = os.WriteFile(path+".meta", meta, 0o644)
		}
		if err != nil {
			t.Errorf("%s: no .meta (run UNITY_META=write go test -run TestUnityPackageMetaFiles .): %v", path, err)
			return nil
		}
		m := unityGUID.FindSubmatch(meta)
		if !bytes.HasPrefix(meta, []byte("fileFormatVersion: 2\n")) || m == nil {
			t.Errorf("%s.meta: not a Unity .meta (fileFormatVersion 2 and a 32-character guid)", path)
			return nil
		}
		if d.IsDir() != bytes.Contains(meta, []byte("\nfolderAsset: yes\n")) {
			t.Errorf("%s.meta: folderAsset must be set for a folder and only for a folder", path)
		}
		if other, dup := seen[string(m[1])]; dup {
			t.Errorf("%s.meta and %s.meta share the GUID %s", path, other, m[1])
		}
		seen[string(m[1])] = path
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range []string{"package.json", "README.md", "LICENSE.md", "NOTICE.md", "Runtime", "Editor"} {
		if _, ok := seenPath(seen, filepath.Join(unityPackage, f)); !ok {
			t.Errorf("the Unity package has no %s with a .meta", f)
		}
	}
	// The package's licence is the samples' own, word for word.
	mine, err1 := os.ReadFile(filepath.Join(unityPackage, "LICENSE.md"))
	theirs, err2 := os.ReadFile("LICENSE")
	if err1 != nil || err2 != nil || !bytes.Equal(mine, theirs) {
		t.Errorf("unity/LICENSE.md must be a copy of the samples' LICENSE: %v %v", err1, err2)
	}
}

func seenPath(seen map[string]string, path string) (string, bool) {
	for g, p := range seen {
		if p == path {
			return g, true
		}
	}
	return "", false
}

// unityMeta is the .meta Unity itself writes for an asset of this kind, with a GUID made from the package name and
// the path inside it.
func unityMeta(path string, dir bool) []byte {
	rel, _ := filepath.Rel(unityPackage, path)
	sum := sha256.Sum256([]byte("com.survivalservers.studio/" + filepath.ToSlash(rel)))
	importer := "DefaultImporter"
	extra := ""
	switch {
	case dir:
		extra = "folderAsset: yes\n"
	case filepath.Base(path) == "package.json":
		importer = "PackageManifestImporter"
	case strings.HasSuffix(path, ".cs"):
		importer = "MonoImporter"
	case strings.HasSuffix(path, ".asmdef"):
		importer = "AssemblyDefinitionImporter"
	case strings.HasSuffix(path, ".md"), strings.HasSuffix(path, ".json"), strings.HasSuffix(path, ".txt"):
		importer = "TextScriptImporter"
	}
	var b strings.Builder
	b.WriteString("fileFormatVersion: 2\nguid: " + hex.EncodeToString(sum[:16]) + "\n" + extra + importer + ":\n")
	b.WriteString("  externalObjects: {}\n")
	if importer == "MonoImporter" {
		b.WriteString("  serializedVersion: 2\n  defaultReferences: []\n  executionOrder: 0\n  icon: {instanceID: 0}\n")
	}
	b.WriteString("  userData:\n  assetBundleName:\n  assetBundleVariant:\n")
	return []byte(b.String())
}
