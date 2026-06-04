// Package source provides adapters that supply base images for dataset generation.
package source

// Ref identifies a single base image from a source.
type Ref struct {
	ID   string // stable identifier (e.g. Picsum ID or filename)
	URL  string // download URL (empty for local)
	Path string // local path (empty for remote)
	MIME string // e.g. "image/jpeg"
}

// Source is the interface implemented by all image-source adapters.
type Source interface {
	// List returns up to n image references, deterministically selected
	// using the provided seed.
	List(seed int64, n int) ([]Ref, error)
	// Fetch downloads (or reads) the image bytes for the given Ref.
	// Implementations may cache by Ref.ID.
	Fetch(ref Ref) ([]byte, error)
}
