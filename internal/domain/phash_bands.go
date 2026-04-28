package domain

// PHashBandCount is the number of bands the 256-bit pHash is split into for the
// LSH pre-filter. Each band is one byte (8 bits) of the hash, indexed by its
// position; a candidate matches a stored cert when at least one band collides.
const PHashBandCount = 32

// PHashBands returns the 32 (band_idx, band_value) pairs of a 256-bit pHash,
// laid out so the repository can persist them and probe a composite index at
// query time without rescanning the whole certificates table.
func PHashBands(phash [32]byte) [PHashBandCount]byte {
	var bands [PHashBandCount]byte
	for i := 0; i < PHashBandCount; i++ {
		bands[i] = phash[i]
	}
	return bands
}
