// Package sqlq builds WHERE clauses from optional filters without string
// interpolation of user input. Conditions use ? placeholders, which are
// renumbered to $n in the order they are added.
package sqlq

import (
	"strconv"
	"strings"
)

type Q struct {
	conds []string
	Args  []any
}

// New starts a builder with optional fixed arguments already bound to $1..$n.
func New(args ...any) *Q { return &Q{Args: append([]any(nil), args...)} }

// Arg binds a value and returns its placeholder.
func (q *Q) Arg(v any) string {
	q.Args = append(q.Args, v)
	return "$" + strconv.Itoa(len(q.Args))
}

// Add appends a condition. Each ? in cond is bound to the next value.
func (q *Q) Add(cond string, vals ...any) *Q {
	var b strings.Builder
	i := 0
	for _, r := range cond {
		if r == '?' && i < len(vals) {
			b.WriteString(q.Arg(vals[i]))
			i++
			continue
		}
		b.WriteRune(r)
	}
	q.conds = append(q.conds, b.String())
	return q
}

// If adds the condition only when ok is true.
func (q *Q) If(ok bool, cond string, vals ...any) *Q {
	if ok {
		q.Add(cond, vals...)
	}
	return q
}

// Where returns " WHERE a AND b" or "".
func (q *Q) Where() string {
	if len(q.conds) == 0 {
		return ""
	}
	return " WHERE " + strings.Join(q.conds, " AND ")
}

// And returns " AND a AND b" for appending to an existing WHERE.
func (q *Q) And() string {
	if len(q.conds) == 0 {
		return ""
	}
	return " AND " + strings.Join(q.conds, " AND ")
}

// Page returns " LIMIT $n OFFSET $m".
func (q *Q) Page(limit, offset int) string {
	return " LIMIT " + q.Arg(limit) + " OFFSET " + q.Arg(offset)
}
