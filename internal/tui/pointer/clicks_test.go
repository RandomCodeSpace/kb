package pointer_test

import (
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/RandomCodeSpace/kb/internal/tui/pointer"
	"github.com/RandomCodeSpace/kb/internal/tui/theme"
)

// testWindow is collapsed. The window length is a scheduling parameter and
// nothing else: classification reads the armed region, and the expiry arrives
// as the message the arming command carries. A test that waited out 400ms of
// wall clock would be asserting the runtime timer, which spec section 10.3.9
// rule 5 bars outright.
const testWindow = 0

// click drives one completed gesture and returns the machine, whether it closed
// a double-click, and the command that arms the next window.
func click(clicks pointer.Clicks, id pointer.ControlID, dragged bool) (pointer.Clicks, bool, tea.Cmd) {
	return clicks.Click(id, dragged, testWindow)
}

// TestARealWindowSchedulesRatherThanDispatches pins the other side of the
// collapse rule without awaiting it.
func TestARealWindowSchedulesRatherThanDispatches(t *testing.T) {
	var clicks pointer.Clicks
	if _, _, arm := clicks.Click("card:a", false, theme.DefaultTiming.DoubleClickWindow); arm == nil {
		t.Fatal("a real window produced no arming command")
	}
	if theme.DefaultTiming.DoubleClickWindow != 400*time.Millisecond {
		t.Fatalf("double-click window = %v, want 400ms", theme.DefaultTiming.DoubleClickWindow)
	}
}

func TestSecondClickOnTheSameRegionIsADoubleClick(t *testing.T) {
	var clicks pointer.Clicks
	clicks, double, arm := click(clicks, "card:a", false)
	if double {
		t.Fatal("the first click classified as a double")
	}
	if arm == nil {
		t.Fatal("the first click did not arm a window")
	}
	if id, armed := clicks.Armed(); !armed || id != "card:a" {
		t.Fatalf("armed region = %q %v", id, armed)
	}
	clicks, double, arm = click(clicks, "card:a", false)
	if !double {
		t.Fatal("the second click on the same region is not a double")
	}
	if arm != nil {
		t.Fatal("a closed double-click armed another window")
	}
	if _, armed := clicks.Armed(); armed {
		t.Fatal("a closed double-click left the window open")
	}
}

func TestADifferentRegionResetsTheWindow(t *testing.T) {
	var clicks pointer.Clicks
	clicks, _, _ = click(clicks, "card:a", false)
	clicks, double, arm := click(clicks, "card:b", false)
	if double {
		t.Fatal("a click on another region classified as a double")
	}
	if arm == nil {
		t.Fatal("the new region did not arm its own window")
	}
	clicks, double, _ = click(clicks, "card:a", false)
	if double {
		t.Fatal("the original region kept a window a different region reset")
	}
	if id, _ := clicks.Armed(); id != "card:a" {
		t.Fatalf("armed region after the third click = %q", id)
	}
}

// TestADragIsNeverADoubleClick is the exclusion spec section 10.3.5 marks as
// not optional: kb's board is a drag-and-drop miller board and a lift that ends
// on its origin must not register as a double-click.
func TestADragIsNeverADoubleClick(t *testing.T) {
	var clicks pointer.Clicks
	clicks, _, _ = click(clicks, "card:a", false)
	clicks, double, arm := click(clicks, "card:a", true)
	if double {
		t.Fatal("a dragged gesture closed a double-click")
	}
	if arm != nil {
		t.Fatal("a dragged gesture armed a window")
	}
	if _, armed := clicks.Armed(); armed {
		t.Fatal("a dragged gesture left a window open")
	}
	clicks, double, _ = click(clicks, "card:a", false)
	if double {
		t.Fatal("the click after a drag closed a double-click")
	}
}

func TestAnEmptyRegionNeverArms(t *testing.T) {
	var clicks pointer.Clicks
	clicks, double, arm := click(clicks, "", false)
	if double || arm != nil {
		t.Fatal("an unidentified region armed the classifier")
	}
	if _, armed := clicks.Armed(); armed {
		t.Fatal("an unidentified region left a window open")
	}
}

func TestResetClosesAnOpenWindow(t *testing.T) {
	var clicks pointer.Clicks
	clicks, _, _ = click(clicks, "card:a", false)
	clicks = clicks.Reset()
	if _, armed := clicks.Armed(); armed {
		t.Fatal("Reset left the window open")
	}
	if _, double, _ := click(clicks, "card:a", false); double {
		t.Fatal("a reset window still closed a double-click")
	}
}

// TestExpireCarriesAGenerationGuard: a window armed after another was scheduled
// must not be closed by the older expiry.
func TestExpireCarriesAGenerationGuard(t *testing.T) {
	var clicks pointer.Clicks
	clicks, _, stale := click(clicks, "card:a", false)
	clicks, _, live := click(clicks, "card:b", false)

	next, handled := clicks.Expire(stale())
	if !handled {
		t.Fatal("Expire did not claim its own message")
	}
	clicks = next
	if id, armed := clicks.Armed(); !armed || id != "card:b" {
		t.Fatalf("a stale expiry closed the live window: %q %v", id, armed)
	}
	clicks, handled = clicks.Expire(live())
	if !handled {
		t.Fatal("Expire did not claim the live message")
	}
	if _, armed := clicks.Armed(); armed {
		t.Fatal("the live expiry did not close the window")
	}
	if _, handled := clicks.Expire(tea.KeyPressMsg{Code: 'j'}); handled {
		t.Fatal("Expire claimed a message it does not own")
	}
}

func TestExpiredWindowClassifiesTheNextClickAsSingle(t *testing.T) {
	var clicks pointer.Clicks
	clicks, _, arm := click(clicks, "card:a", false)
	clicks, _ = clicks.Expire(arm())
	if _, double, _ := click(clicks, "card:a", false); double {
		t.Fatal("a click past the window classified as a double")
	}
}

// TestCollapsedWindowClassifiesEveryClickAsSingle is the collapse rule: the
// arming command dispatches immediately, so a collapsed program never sees a
// double-click - the deterministic reading a golden needs.
func TestCollapsedWindowClassifiesEveryClickAsSingle(t *testing.T) {
	var clicks pointer.Clicks
	window := theme.TimingCollapsed.DoubleClickWindow
	clicks, _, arm := clicks.Click("card:a", false, window)
	if arm == nil {
		t.Fatal("collapsed timing produced no arming command")
	}
	next, handled := clicks.Expire(arm())
	if !handled {
		t.Fatal("collapsed arming did not dispatch its own expiry")
	}
	clicks = next
	if _, double, _ := clicks.Click("card:a", false, window); double {
		t.Fatal("collapsed timing classified a double-click")
	}
}
