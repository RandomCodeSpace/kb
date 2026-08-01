package store

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"testing"
)

func TestLoadOrCreateSecretEnvironmentBypassesFilesystem(t *testing.T) {
	want := []byte("environment-secret-value")
	t.Setenv("KB_SECRET", string(want))
	dataDir := filepath.Join(t.TempDir(), "missing", "data")
	got, err := LoadOrCreateSecret(dataDir)
	if err != nil {
		t.Fatalf("LoadOrCreateSecret: %v", err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("LoadOrCreateSecret = %x, want environment value %x", got, want)
	}
	if _, err := os.Stat(dataDir); !os.IsNotExist(err) {
		t.Fatalf("environment secret touched data directory: %v", err)
	}
}

func TestCreateSecretConcurrentPublication(t *testing.T) {
	t.Setenv("KB_SECRET", "")
	dataDir := t.TempDir()
	path := filepath.Join(dataDir, "secret")
	const creators = 8

	ready := make(chan struct{}, creators)
	release := make(chan struct{})
	type result struct {
		secret []byte
		err    error
	}
	results := make(chan result, creators)
	candidates := make([][]byte, creators)
	for i := range creators {
		candidate := bytes.Repeat([]byte{byte(i + 1)}, secretFileBytes)
		candidates[i] = candidate
		go func() {
			secret, err := createSecret(dataDir, path, bytes.NewReader(candidate), func() error {
				ready <- struct{}{}
				<-release
				return nil
			})
			results <- result{secret: secret, err: err}
		}()
	}

	for range creators {
		<-ready
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("secret published before barrier release: %v", err)
	}
	temps, err := filepath.Glob(filepath.Join(dataDir, secretTempPattern))
	if err != nil {
		t.Fatalf("glob secret temp files: %v", err)
	}
	if len(temps) != creators {
		t.Fatalf("complete temp files = %d, want %d", len(temps), creators)
	}
	for _, tempPath := range temps {
		info, err := os.Stat(tempPath)
		if err != nil {
			t.Fatalf("stat secret temp file: %v", err)
		}
		if info.Size() != secretFileBytes {
			t.Errorf("secret temp size = %d, want %d", info.Size(), secretFileBytes)
		}
		if perm := info.Mode().Perm(); perm != 0o600 {
			t.Errorf("secret temp mode = %o, want 600", perm)
		}
	}
	close(release)

	got := make([][]byte, 0, creators)
	for range creators {
		res := <-results
		if res.err != nil {
			t.Fatalf("createSecret: %v", res.err)
		}
		got = append(got, res.secret)
	}
	final, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read final secret: %v", err)
	}
	if len(final) != secretFileBytes {
		t.Fatalf("final secret size = %d, want %d", len(final), secretFileBytes)
	}
	matchedCandidate := false
	for _, candidate := range candidates {
		matchedCandidate = matchedCandidate || bytes.Equal(final, candidate)
	}
	if !matchedCandidate {
		t.Fatal("final secret does not match any complete candidate")
	}
	for i, secret := range got {
		if !bytes.Equal(secret, final) {
			t.Errorf("creator %d returned %x, want final %x", i, secret, final)
		}
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat final secret: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Errorf("final secret mode = %o, want 600", perm)
	}
	temps, err = filepath.Glob(filepath.Join(dataDir, secretTempPattern))
	if err != nil {
		t.Fatalf("glob secret temp files after publication: %v", err)
	}
	if len(temps) != 0 {
		t.Errorf("secret temp files leaked: %v", temps)
	}
}

func TestCreateSecretPrepublishFailureCleansTemp(t *testing.T) {
	dataDir := t.TempDir()
	path := filepath.Join(dataDir, "secret")
	wantErr := errors.New("injected publication failure")
	_, err := createSecret(dataDir, path, bytes.NewReader(bytes.Repeat([]byte("x"), secretFileBytes)), func() error {
		return wantErr
	})
	if !errors.Is(err, wantErr) {
		t.Fatalf("createSecret error = %v, want %v", err, wantErr)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("final secret exists after prepublication failure: %v", err)
	}
	temps, err := filepath.Glob(filepath.Join(dataDir, secretTempPattern))
	if err != nil {
		t.Fatalf("glob secret temp files: %v", err)
	}
	if len(temps) != 0 {
		t.Errorf("secret temp files leaked after prepublication failure: %v", temps)
	}
}

func TestLoadOrCreateSecretInvalidFinalIsUnchanged(t *testing.T) {
	t.Setenv("KB_SECRET", "")
	for _, size := range []int{0, 1, secretFileBytes - 1} {
		t.Run(strconv.Itoa(size), func(t *testing.T) {
			dataDir := t.TempDir()
			path := filepath.Join(dataDir, "secret")
			want := bytes.Repeat([]byte{byte(size + 1)}, size)
			if err := os.WriteFile(path, want, 0o640); err != nil {
				t.Fatalf("write invalid secret: %v", err)
			}
			before, err := os.Stat(path)
			if err != nil {
				t.Fatalf("stat invalid secret fixture: %v", err)
			}

			if got, err := LoadOrCreateSecret(dataDir); err == nil {
				t.Fatalf("LoadOrCreateSecret accepted invalid final as %x", got)
			}
			final, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("read invalid final after failure: %v", err)
			}
			if !bytes.Equal(final, want) {
				t.Fatalf("invalid final changed to %x, want %x", final, want)
			}
			info, err := os.Stat(path)
			if err != nil {
				t.Fatalf("stat invalid final after failure: %v", err)
			}
			if perm := info.Mode().Perm(); perm != before.Mode().Perm() {
				t.Errorf("invalid final mode = %o, want unchanged %o", perm, before.Mode().Perm())
			}
			temps, err := filepath.Glob(filepath.Join(dataDir, secretTempPattern))
			if err != nil {
				t.Fatalf("glob secret temp files: %v", err)
			}
			if len(temps) != 0 {
				t.Errorf("secret temp files created for invalid final: %v", temps)
			}
		})
	}
}
