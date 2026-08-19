package carddetail

import (
	"sync"

	"github.com/RandomCodeSpace/kb/internal/tui/theme"
)

// testStyles is the design system every detail test renders through. It is
// built once: theme.New resolves the whole palette and builds both variants.
var testStyles = sync.OnceValue(func() *theme.Styles { return theme.New(true) })
