// Package names suggests a name for a new jail or guest, in Jail Deck's own
// style: an adjective and an animal, "jolly-otter" (#148). It suggests and
// never assigns: creating anything still takes the name the caller gives.
package names

import (
	"context"
	"errors"
	"fmt"
	"math/rand/v2"
)

// draws is how many pairs are tried before a suggestion gains a number. With
// some 17,000 pairs and a host's jails and guests in the hundreds at most, the
// first draw is nearly always free.
const draws = 20

// InUse reads the names in use on this host -- jails, guests and templates --
// once per suggestion, rather than once per draw.
type InUse interface {
	InUse(ctx context.Context) (map[string]bool, error)
}

// Suggester draws names. rand is a field so that the tests can make the draws
// predictable.
type Suggester struct {
	inUse InUse
	rand  *rand.Rand
}

func NewSuggester(inUse InUse) *Suggester {
	return &Suggester{inUse: inUse, rand: rand.New(rand.NewPCG(rand.Uint64(), rand.Uint64()))}
}

// ErrNoName is a host where nothing free turned up, numbered or not.
var ErrNoName = errors.New("no free name turned up")

// Suggest answers a free name: a pair, or after draws taken pairs, a pair
// with a two-digit number, "amazing-spider-42".
func (s *Suggester) Suggest(ctx context.Context) (string, error) {
	taken, err := s.inUse.InUse(ctx)
	if err != nil {
		return "", err
	}
	for i := 0; i < 2*draws; i++ {
		if name := s.draw(i); !taken[name] {
			return name, nil
		}
	}
	return "", ErrNoName
}

// draw is the i-th try: a pair, numbered from the draws-th on.
func (s *Suggester) draw(i int) string {
	name := adjectives[s.rand.IntN(len(adjectives))] + "-" + animals[s.rand.IntN(len(animals))]
	if i >= draws {
		name = fmt.Sprintf("%s-%02d", name, 10+s.rand.IntN(90))
	}
	return name
}
