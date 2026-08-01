package store

import (
	"bytes"
	"errors"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"testing"
)

type orderedSecretReader struct {
	events *[]string
}

func (r orderedSecretReader) Read(p []byte) (int, error) {
	*r.events = append(*r.events, "random")
	for i := range p {
		p[i] = 'x'
	}
	return len(p), nil
}

type orderedSecretTemp struct {
	events *[]string
}

func (f orderedSecretTemp) Name() string { return "temp" }
func (f orderedSecretTemp) Write(p []byte) (int, error) {
	*f.events = append(*f.events, "write")
	return len(p), nil
}
func (f orderedSecretTemp) Chmod(mode os.FileMode) error {
	*f.events = append(*f.events, "chmod:"+mode.String())
	return nil
}
func (f orderedSecretTemp) Sync() error {
	*f.events = append(*f.events, "file sync")
	return nil
}
func (f orderedSecretTemp) Close() error {
	*f.events = append(*f.events, "file close")
	return nil
}

type orderedSecretDir struct {
	events *[]string
}

func (d orderedSecretDir) Sync() error {
	*d.events = append(*d.events, "dir sync")
	return nil
}
func (d orderedSecretDir) Close() error {
	*d.events = append(*d.events, "dir close")
	return nil
}

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

func TestCreateSecretPublicationOrder(t *testing.T) {
	installCoverageSecretOps(t)
	events := []string{}
	createSecretTemp = func(dir, pattern string) (secretTempFile, error) {
		events = append(events, "create temp:"+dir+":"+pattern)
		return orderedSecretTemp{events: &events}, nil
	}
	linkSecretFile = func(oldname, newname string) error {
		events = append(events, "link:"+oldname+":"+newname)
		return nil
	}
	openSecretDir = func(path string) (secretDirectory, error) {
		events = append(events, "open dir:"+path)
		return orderedSecretDir{events: &events}, nil
	}
	removeSecretFile = func(path string) error {
		events = append(events, "remove:"+path)
		return nil
	}

	got, err := createSecret("data", "final", orderedSecretReader{events: &events}, func() error {
		events = append(events, "before publish")
		return nil
	})
	if err != nil {
		t.Fatalf("createSecret: %v", err)
	}
	if want := bytes.Repeat([]byte{'x'}, secretFileBytes); !bytes.Equal(got, want) {
		t.Fatalf("createSecret = %x, want %x", got, want)
	}
	wantEvents := []string{
		"random",
		"create temp:data:" + secretTempPattern,
		"write",
		"chmod:-rw-------",
		"file sync",
		"file close",
		"before publish",
		"link:temp:final",
		"open dir:data",
		"dir sync",
		"dir close",
		"remove:temp",
	}
	if !reflect.DeepEqual(events, wantEvents) {
		t.Fatalf("publication events = %v, want %v", events, wantEvents)
	}
}

func TestCreateSecretLosingPublisherCleansBeforeAdoptingWinner(t *testing.T) {
	installCoverageSecretOps(t)
	events := []string{}
	winner := bytes.Repeat([]byte{'w'}, secretFileBytes)
	createSecretTemp = func(string, string) (secretTempFile, error) {
		return orderedSecretTemp{events: &events}, nil
	}
	linkSecretFile = func(string, string) error {
		events = append(events, "link")
		return os.ErrExist
	}
	removeSecretFile = func(string) error {
		events = append(events, "remove")
		return nil
	}
	readSecretFile = func(string) ([]byte, error) {
		events = append(events, "read winner")
		return winner, nil
	}

	got, err := createSecret("data", "final", io.LimitReader(orderedSecretReader{events: &events}, secretFileBytes), func() error {
		events = append(events, "before publish")
		return nil
	})
	if err != nil {
		t.Fatalf("createSecret: %v", err)
	}
	if !bytes.Equal(got, winner) {
		t.Fatalf("createSecret = %x, want winner %x", got, winner)
	}
	wantTail := []string{"before publish", "link", "remove", "read winner"}
	if len(events) < len(wantTail) || !reflect.DeepEqual(events[len(events)-len(wantTail):], wantTail) {
		t.Fatalf("losing publication events = %v, want tail %v", events, wantTail)
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
