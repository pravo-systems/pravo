package extract

// Options decide what is worth a definition.
type Options struct {
	// MinInstances is the fewest resources that make a definition.
	MinInstances int
	// MinSaving is the least a definition must save, in percent of the bytes of its resources.
	MinSaving float64
	// MergeGain: two groups are joined only when the join saves more than this many bytes.
	MergeGain int
	// DefaultShare: a value that this share of the instances has becomes the default of its parameter.
	DefaultShare float64
	// MergeNumbers: numbers that agree in every instance share a parameter only with this many
	// different values.
	MergeNumbers int
	// MaxParams: a definition with more parameters than this is not written.
	MaxParams int
	// Width: an instance longer than this is written over several lines.
	Width int
	// NoAffix, NoMerge and NoDefaults turn off the cutting of shared text, the sharing of one
	// parameter between holes that agree, and default values.
	NoAffix, NoMerge, NoDefaults bool
	// RefParams lets a parameter carry a reference to another resource.
	RefParams bool
	// Only and Skip select resource types and labels; Pick selects definitions by name.
	Only, Skip, Pick map[string]bool
	// Rename gives a definition another name.
	Rename map[string]string
}

// DefaultOptions are what pravo shadow --fold uses.
func DefaultOptions() *Options {
	return &Options{MinInstances: 3, MinSaving: 15, DefaultShare: 0.6, MergeNumbers: 3, MaxParams: 8, Width: 110}
}
