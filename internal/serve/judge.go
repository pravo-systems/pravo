package serve

// The judge is the one place that decides what a value of a node is held against.
// Every surface that sends a value of a node (the attributes of its record and of
// its history, the changes of a plan, the outputs, the search index, the choices
// of the template form) asks it, so that one node and one field get one answer on
// every page. A test (TestOnlyTheJudgeChoosesTheStrings) fails when another file
// reaches for the secrets of the model.
//
// A value is withheld for one of three reasons, in this order:
//
//   - it is marked: the schema says the attribute is sensitive or write-only, its
//     name says it is a secret, or the type has no schema (walk.attribute);
//   - it is read: the specification says that the field reads a withheld attribute
//     of another node, or a field that is itself derived, however its text reads
//     (computeTaint). This is the way a typed string travels, a password or a
//     region that a person wrote into a sensitive attribute: along the references
//     that read it, and by no other way, since the same text in a node that does
//     not read it is a coincidence;
//   - it holds a string that a provider made (random_password.result): such a string
//     is unguessable, so it is looked for in every value, whatever route it took
//     (a file that a data source reads back, an attribute that its provider echoes).
//
// A data source reads from outside the graph, and no reference leads from a file
// that a resource wrote to what the data source reads from it. So the values of a
// data source are also held against the strings of the nodes it depends on, typed
// or made, and a field of it that holds one seeds the taint: whatever reads that
// field is withheld by the second reason, with no text to look at.
//
// A node that the specification does not declare has no references to follow, and
// is held against the strings of the nodes it depends on as the state records them.

// secretsFor is the set of sensitive strings that a response about the plan po is
// judged against: those of the state and the ones the plan would write. A plan
// that has none of its own (the plan of the graph, which adds its secrets to the
// model's set, or no plan) is judged against the model's.
func (m *model) secretsFor(po *planOverlay) *secretSet {
	if po != nil && po.secrets != nil {
		return po.secrets
	}
	return m.secrets
}

// judgement is how the values of one node are held: the top-level fields that are
// withheld for what they read (fields), and the strings that a value that is left
// is looked at for (text).
type judgement struct {
	fields map[string]bool
	text   *secretSet
	// because says what each of the fields reads, and textWhy what a value that holds
	// one of the strings of text is withheld for: sentences for the page.
	because map[string]string
	textWhy string
}

// judge returns the judgement of the node at addr, against the secrets of the plan
// po (the model's when there is none).
func (m *model) judge(po *planOverlay, addr string) judgement {
	sec := m.secretsFor(po)
	if fields, declared := m.taintOf(addr); declared {
		why := "The text holds a secret that a provider made."
		if mn := m.nodes[addr]; mn != nil && mn.Mode == "data" {
			why = "The text holds a secret that a provider made or one of a node this data source depends on."
		}
		return judgement{fields: fields, text: m.declaredText(sec, addr), because: m.because[addr], textWhy: why}
	}
	return judgement{text: m.undeclaredText(sec, addr), textWhy: "The text holds a secret of this node or of one it depends on, or one that a provider made."}
}

// walk returns the walk that renders the values of the node with this judgement.
func (j judgement) walk(build bool) *walk {
	return &walk{secrets: j.text, taint: j.fields, because: j.because, textWhy: j.textWhy, build: build}
}

// declaredText is what the values of a node that the specification declares are
// looked at for: what the providers made, and for a data source the strings of the
// nodes it depends on besides. The strings of the node itself are not among them:
// its own sensitive attributes are withheld for what they are, and the value of an
// SSM parameter says nothing of its ARN.
func (m *model) declaredText(sec *secretSet, addr string) *secretSet {
	if mn := m.nodes[addr]; mn != nil && mn.Mode == "data" {
		return sec.withMade(m.scopeOf(addr), addr)
	}
	return sec.madeOnly()
}

// undeclaredText is declaredText for a node that has no references to follow: its own
// strings and those of the nodes it depends on, as the state records them, for every
// kind of node. With nothing to say which field was set from which, a connection
// string that holds the password of the same record is what it looks like. An output
// that the state records and the specification does not declare has not even those,
// so what it holds is judged against every string.
func (m *model) undeclaredText(sec *secretSet, addr string) *secretSet {
	if mn := m.nodes[addr]; mn != nil && mn.isOutput() {
		return sec
	}
	return sec.withMade(m.scopeOf(addr), "")
}

// spanText is what the name and the attributes of a span of a run are held
// against: a name, a host or a path is not a value of a record, so what the state
// keeps as typed (a region) is no reason to hide it; a string that a provider made
// is unguessable and is hidden wherever it shows.
func spanText(sec *secretSet) *secretSet { return sec.madeOnly() }

// echoText is what the record of a data source is looked at for when the taint is
// worked out, which is before any plan is laid over the model.
func (m *model) echoText(addr string) *secretSet { return m.declaredText(m.secrets, addr) }

// literals is what a text that a person wrote about the node is looked at for: a
// line of its source, a value the specification sets on it, a sentence about it.
// Such a text repeats what the person typed, so it is held against the strings of
// the node and of what it depends on, besides what the providers made.
func (m *model) literals(po *planOverlay, addr string) *secretSet {
	sec := m.secretsFor(po)
	if mn := m.nodes[addr]; mn != nil && mn.isOutput() && mn.out == nil {
		return sec
	}
	return sec.withMade(m.scopeOf(addr), "")
}
