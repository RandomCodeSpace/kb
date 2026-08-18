package server

import (
	"sync"
	"testing"

	"github.com/RandomCodeSpace/kb/internal/forge"
)

func TestSharedForgeInitializesOnceUnderConcurrency(t *testing.T) {
	s := newServer(Config{}, testStatic, newTestStore(t))
	const callers = 64
	start := make(chan struct{})
	services := make(chan *forge.Service, callers)
	var wg sync.WaitGroup
	for range callers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			services <- s.sharedForge()
		}()
	}
	close(start)
	wg.Wait()
	close(services)
	var first *forge.Service
	for service := range services {
		if first == nil {
			first = service
		}
		if service != first {
			t.Fatal("sharedForge returned multiple services")
		}
	}
}
