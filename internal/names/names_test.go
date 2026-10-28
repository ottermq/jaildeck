package names

import (
	"context"
	"errors"
	"math/rand/v2"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/ottermq/jaildeck/internal/jails"
)

type inUse map[string]bool

func (u inUse) InUse(context.Context) (map[string]bool, error) { return u, nil }

type unreadable struct{}

func (unreadable) InUse(context.Context) (map[string]bool, error) {
	return nil, errors.New("jls: permission denied")
}

func seeded(u InUse, seed uint64) *Suggester {
	return &Suggester{inUse: u, rand: rand.New(rand.NewPCG(seed, seed))}
}

// vm-bhyve's rule for a guest's name, from util::check_name, as internal/vms
// holds it; and a hostname label.
var (
	vmName   = regexp.MustCompile(`^(?i)[a-z0-9][.a-z0-9_-]{0,229}[a-z0-9]$`)
	hostname = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?$`)
	word     = regexp.MustCompile(`^[a-z]+$`)
)

// Every pair the lists can make, and every one of them with the number a full
// host adds, is a jail's name, a guest's and a hostname, within 24 characters.
func TestEveryNameIsValidEverywhere(t *testing.T) {
	for _, list := range [][]string{adjectives, animals} {
		for _, w := range list {
			if !word.MatchString(w) {
				t.Errorf("%q is not lowercase letters only", w)
			}
		}
	}
	pairs := 0
	for _, a := range adjectives {
		for _, n := range animals {
			for _, name := range []string{a + "-" + n, a + "-" + n + "-99"} {
				if _, err := jails.NewJailName(name); err != nil || !vmName.MatchString(name) ||
					!hostname.MatchString(name) || len(name) > 24 {
					t.Fatalf("%q is not valid everywhere (len %d)", name, len(name))
				}
			}
			pairs++
		}
	}
	if pairs < 15000 {
		t.Errorf("only %d pairs", pairs)
	}
}

func TestNoWordTwiceAndNoStateWords(t *testing.T) {
	for _, list := range [][]string{adjectives, animals} {
		sorted := slices.Clone(list)
		slices.Sort(sorted)
		if compacted := slices.Compact(slices.Clone(sorted)); len(compacted) != len(list) {
			t.Errorf("a word appears twice in %v", list[:3])
		}
	}
	// What a jail or guest can be: a name starting with one would be misread
	// on every page that shows its state beside it.
	for _, state := range []string{"running", "stopped", "failed", "locked", "ready", "idle", "dying",
		"suspended", "crashed", "dead", "broken", "busy", "pending", "error"} {
		if slices.Contains(adjectives, state) {
			t.Errorf("%q is a state, not an adjective for a name", state)
		}
	}
	if !slices.Contains(animals, "otter") {
		t.Error("no otter")
	}
}

func TestASuggestionIsFreeAndAPair(t *testing.T) {
	s := NewSuggester(inUse{})
	name, err := s.Suggest(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	adjective, animal, ok := strings.Cut(name, "-")
	if !ok || !slices.Contains(adjectives, adjective) || !slices.Contains(animals, animal) {
		t.Errorf("suggestion %q is not an adjective and an animal", name)
	}
}

// A name in use is never suggested: the draws that would have produced it are
// replayed with the same seed and all marked taken but the last.
func TestATakenNameIsNeverSuggested(t *testing.T) {
	twin := seeded(inUse{}, 7)
	taken := inUse{}
	var free string
	for i := 0; i < 5; i++ {
		name := twin.draw(i)
		if i < 4 {
			taken[name] = true
		} else {
			free = name
		}
	}
	got, err := seeded(taken, 7).Suggest(context.Background())
	if err != nil || got != free {
		t.Errorf("suggested %q, %v; want the fifth draw, %q", got, err, free)
	}
}

// A host where every draw is taken gets a pair with a number, and only then.
func TestAFullHostGetsANumber(t *testing.T) {
	every := inUse{}
	for _, a := range adjectives {
		for _, n := range animals {
			every[a+"-"+n] = true
		}
	}
	name, err := NewSuggester(every).Suggest(context.Background())
	if err != nil || !regexp.MustCompile(`^[a-z]+-[a-z]+-[1-9][0-9]$`).MatchString(name) {
		t.Errorf("suggested %q, %v; want a pair with a two-digit number", name, err)
	}
}

func TestNothingFreeAndNothingReadableAreErrors(t *testing.T) {
	// Replay all forty draws, numbered ones included, and take them all.
	twin := seeded(inUse{}, 3)
	taken := inUse{}
	for i := 0; i < 2*draws; i++ {
		taken[twin.draw(i)] = true
	}
	if _, err := seeded(taken, 3).Suggest(context.Background()); !errors.Is(err, ErrNoName) {
		t.Errorf("err = %v, want ErrNoName", err)
	}
	if _, err := NewSuggester(unreadable{}).Suggest(context.Background()); err == nil ||
		!strings.Contains(err.Error(), "permission denied") {
		t.Errorf("err = %v, want the host's own error", err)
	}
}
