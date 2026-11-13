// Checks every sample's Dockerfile: the image a game server runs in must trust the usual certificate authorities, or
// any HTTPS call the game makes (a sign-in, telemetry, the studio's own backend) fails. Debian and Ubuntu base images
// ship none, so their samples install ca-certificates. Distroless images carry the bundle, and Node trusts its own
// built-in list.
package samples

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

var bringsOwnCAs = regexp.MustCompile(`^(gcr\.io/distroless/|node:)`)

func TestEveryImageTrustsTheUsualCertificates(t *testing.T) {
	files, err := filepath.Glob("*/Dockerfile")
	if err != nil || len(files) == 0 {
		t.Fatalf("no sample Dockerfiles found (%v)", err)
	}
	from := regexp.MustCompile(`(?m)^FROM\s+(\S+)`)
	for _, f := range files {
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		s := string(b)
		m := from.FindAllStringSubmatchIndex(s, -1)
		if len(m) == 0 {
			t.Errorf("%s: no FROM line", f)
			continue
		}
		last := m[len(m)-1] // the stage the game server runs in
		base, stage := s[last[2]:last[3]], s[last[0]:]
		switch {
		case bringsOwnCAs.MatchString(base):
		case strings.HasPrefix(base, "debian:") || strings.HasPrefix(base, "ubuntu:"):
			if !strings.Contains(stage, "ca-certificates") {
				t.Errorf("%s: the %s image has no CA certificates; install ca-certificates in its last stage", f, base)
			}
		default:
			t.Errorf("%s: base %s is not one this test knows; add it here with how it gets CA certificates", f, base)
		}
	}
}
