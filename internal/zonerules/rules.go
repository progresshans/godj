package zonerules

import (
	"cmp"
	"encoding/binary"
	"fmt"
	"iter"
	"slices"
	"strings"
	"time"
)

// Recurring Gregorian rules repeat after 400 years, including leap centuries.
// The pinned archive's last explicit transition is in 2087. Keep all history
// through 2400 and one complete future cycle instead of expanding to year 9999.
const (
	CycleStart         int64 = 13569465600 // 2400-01-01T00:00:00Z
	CycleSeconds       int64 = 146097 * 86400
	minimumEpoch       int64 = -62167219200 // 0000-01-01T00:00:00Z; guards model boundaries.
	maximumTransitions       = 4096
)

// Transition is a value copy. LocalStart is the civil boundary for fold=0:
// gaps and overlaps both retain the offset from before the transition.
type Transition struct {
	Start, LocalStart int64
	Offset            int
}

type ruleData struct{ transitions []Transition }
type Rules struct{ data *ruleData }

func Load(name string) (Rules, error) {
	location, err := Borrow(name)
	if err != nil {
		return Rules{}, err
	}
	data, err := database()
	if err != nil {
		return Rules{}, err
	}
	entry := data.entries[name]
	entry.rulesOnce.Do(func() {
		raw, err := entry.read()
		if err != nil {
			entry.rulesErr = err
			return
		}
		entry.rules, entry.rulesErr = readRules(raw, location)
	})
	if entry.rulesErr != nil {
		return Rules{}, entry.rulesErr
	}
	return Rules{data: entry.rules}, nil
}

func (rules Rules) Transitions() iter.Seq[Transition] {
	return func(yield func(Transition) bool) {
		if rules.data == nil {
			return
		}
		for _, transition := range rules.data.transitions {
			if !yield(transition) {
				return
			}
		}
	}
}

func (rules Rules) OffsetAt(utcSeconds int64) (int, error) { return rules.offset(utcSeconds, false) }
func (rules Rules) OffsetForCivil(civilSeconds int64) (int, error) {
	return rules.offset(civilSeconds, true)
}

func (rules Rules) offset(seconds int64, civil bool) (int, error) {
	if rules.data == nil || seconds < minimumEpoch {
		return 0, ErrInvalid
	}
	if seconds >= CycleStart {
		seconds = CycleStart + (seconds-CycleStart)%CycleSeconds
	}
	index, exact := slices.BinarySearchFunc(rules.data.transitions, seconds, func(transition Transition, instant int64) int {
		if civil {
			return cmp.Compare(transition.LocalStart, instant)
		}
		return cmp.Compare(transition.Start, instant)
	})
	if !exact {
		index--
	}
	if index < 0 {
		return 0, ErrInvalid
	}
	return rules.data.transitions[index].Offset, nil
}

type rawTransition struct {
	at     int64
	offset int
}

func readRules(raw []byte, location *time.Location) (*ruleData, error) {
	history, footer, err := readTZif(raw)
	if err != nil {
		return nil, err
	}
	recurring, err := parseRecurrence(footer)
	if err != nil {
		return nil, fmt.Errorf("%w: invalid TZif recurrence", err)
	}
	_, first := time.Unix(minimumEpoch, 0).In(location).Zone()
	data := &ruleData{transitions: []Transition{{Start: minimumEpoch, LocalStart: minimumEpoch, Offset: first}}}
	last := minimumEpoch - 1
	if len(history) != 0 {
		last = history[len(history)-1].at
		if last >= CycleStart {
			return nil, fmt.Errorf("%w: explicit history exceeds the recurring-cycle boundary", ErrUnavailable)
		}
	}
	if recurring.dst {
		firstYear := time.Unix(max(last, minimumEpoch), 0).UTC().Year() - 1
		// Include the following year so extended/negative rule hours crossing
		// the cycle boundary are present for both UTC and civil lookup.
		for year := firstYear; year <= 2801; year++ {
			for _, transition := range []rawTransition{
				{recurring.start.instant(year, recurring.standard), recurring.daylight},
				{recurring.end.instant(year, recurring.daylight), recurring.standard},
			} {
				if transition.at > last {
					history = append(history, transition)
				}
			}
		}
	}
	slices.SortFunc(history, func(a, b rawTransition) int { return cmp.Compare(a.at, b.at) })
	previous := first
	for index, transition := range history {
		if index > 0 && transition.at <= history[index-1].at {
			return nil, fmt.Errorf("%w: duplicate transition", ErrUnavailable)
		}
		if transition.at < minimumEpoch {
			continue
		}
		if transition.offset == previous {
			continue
		}
		local := transition.at + int64(max(previous, transition.offset))
		if local <= data.transitions[len(data.transitions)-1].LocalStart {
			return nil, fmt.Errorf("%w: civil transition order is ambiguous", ErrUnavailable)
		}
		data.transitions = append(data.transitions, Transition{Start: transition.at, LocalStart: local, Offset: transition.offset})
		previous = transition.offset
		if len(data.transitions) > maximumTransitions {
			return nil, fmt.Errorf("%w: transition budget exceeded", ErrUnavailable)
		}
	}
	if !recurring.dst && previous != recurring.standard {
		return nil, fmt.Errorf("%w: fixed footer differs from final offset", ErrUnavailable)
	}
	return data, nil
}

// Read the 64-bit block of RFC 9636 TZif. Leap-second archives are deliberately
// outside this pinned POSIX/UTC model; a source change must not reinterpret them.
func readTZif(raw []byte) ([]rawTransition, string, error) {
	if len(raw) < 44 || string(raw[:4]) != "TZif" || raw[4] != '2' && raw[4] != '3' && raw[4] != '4' {
		return nil, "", ErrUnavailable
	}
	header := func(at, width int) ([6]int, int, error) {
		var counts [6]int
		if at < 0 || at+44 > len(raw) || string(raw[at:at+4]) != "TZif" {
			return counts, 0, ErrUnavailable
		}
		for index := range counts {
			value := binary.BigEndian.Uint32(raw[at+20+index*4 : at+24+index*4])
			if value > maximumZoneBytes {
				return counts, 0, ErrUnavailable
			}
			counts[index] = int(value)
		}
		ut, standard, leaps, times, types, chars := counts[0], counts[1], counts[2], counts[3], counts[4], counts[5]
		if leaps != 0 || types < 1 || types > 256 || times > maximumTransitions || ut != 0 && ut != types || standard != 0 && standard != types {
			return counts, 0, ErrUnavailable
		}
		end := at + 44 + times*(width+1) + types*6 + chars + standard + ut
		if end > len(raw) {
			return counts, 0, ErrUnavailable
		}
		return counts, end, nil
	}
	_, block, err := header(0, 4)
	if err != nil {
		return nil, "", err
	}
	counts, end, err := header(block, 8)
	if err != nil {
		return nil, "", err
	}
	times, types, chars := counts[3], counts[4], counts[5]
	start := block + 44
	indexes := start + times*8
	offsets := indexes + times
	values := make([]int, types)
	for index := range values {
		at := offsets + index*6
		values[index] = int(int32(binary.BigEndian.Uint32(raw[at : at+4])))
		if raw[at+4] > 1 || int(raw[at+5]) >= chars {
			return nil, "", ErrUnavailable
		}
	}
	history := make([]rawTransition, times)
	for index := range history {
		kind := int(raw[indexes+index])
		if kind >= len(values) {
			return nil, "", ErrUnavailable
		}
		history[index] = rawTransition{at: int64(binary.BigEndian.Uint64(raw[start+index*8 : start+(index+1)*8])), offset: values[kind]}
		if index > 0 && history[index].at <= history[index-1].at {
			return nil, "", ErrUnavailable
		}
	}
	if end+2 > len(raw) || raw[end] != '\n' || raw[len(raw)-1] != '\n' {
		return nil, "", ErrUnavailable
	}
	footer := string(raw[end+1 : len(raw)-1])
	if footer == "" || strings.ContainsAny(footer, "\n\r\x00") {
		return nil, "", ErrUnavailable
	}
	return history, footer, nil
}
