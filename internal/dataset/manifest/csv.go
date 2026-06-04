package manifest

import (
	"encoding/csv"
	"fmt"
	"os"
	"strconv"
)

// WriteCSV writes a flat CSV mirror of m to path.
// Columns: id, base_image_id, transform_family, expected_match, confidence,
//
//	borderline, mime, sha256, output_path, is_negative_control, peer_base_id
func WriteCSV(path string, m *Manifest) error {
	f, err := os.Create(path)
	if err != nil {
		return fmt.Errorf("manifest csv: create %q: %w", path, err)
	}
	defer f.Close()

	w := csv.NewWriter(f)
	header := []string{
		"id", "base_image_id", "transform_family",
		"expected_match", "confidence", "borderline",
		"mime", "sha256", "output_path",
		"is_negative_control", "peer_base_id",
	}
	if err := w.Write(header); err != nil {
		return err
	}
	for _, s := range m.Samples {
		row := []string{
			s.ID,
			s.BaseImageID,
			s.TransformFamily,
			strconv.FormatBool(s.ExpectedMatch),
			s.Confidence,
			strconv.FormatBool(s.Borderline),
			s.MIME,
			s.SHA256,
			s.OutputPath,
			strconv.FormatBool(s.IsNegControl),
			s.PeerBaseID,
		}
		if err := w.Write(row); err != nil {
			return err
		}
	}
	w.Flush()
	return w.Error()
}
