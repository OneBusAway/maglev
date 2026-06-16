package utils

import "testing"

func TestEncodePolyline_GoogleExample(t *testing.T) {
	// Canonical example from Google's polyline algorithm documentation.
	coords := [][]float64{
		{38.5, -120.2},
		{40.7, -120.95},
		{43.252, -126.453},
	}
	got := EncodePolyline(coords)
	want := "_p~iF~ps|U_ulLnnqC_mqNvxq`@"
	if got != want {
		t.Errorf("EncodePolyline() = %q, want %q", got, want)
	}
}

func TestEncodePolyline_FloorsNotRounds(t *testing.T) {
	// 0.000019 * 1e5 = 1.9. Java floors to 1 (same as 0.00001); a rounding
	// encoder would yield 2 (same as 0.00002). Verify we floor.
	got := EncodePolyline([][]float64{{0.000019, 0}})
	floored := EncodePolyline([][]float64{{0.00001, 0}})
	rounded := EncodePolyline([][]float64{{0.00002, 0}})
	if got != floored {
		t.Errorf("floor(0.000019*1e5) should match 0.00001 encoding; got %q want %q", got, floored)
	}
	if got == rounded {
		t.Errorf("floored encoding should differ from rounded 0.00002 encoding %q", rounded)
	}
}

func TestEncodePolyline_PreservesDuplicates(t *testing.T) {
	// A consecutive duplicate point must still be encoded (delta 0,0), not dropped.
	coords := [][]float64{{1.0, 1.0}, {1.0, 1.0}, {2.0, 2.0}}
	withDup := EncodePolyline(coords)
	withoutDup := EncodePolyline([][]float64{{1.0, 1.0}, {2.0, 2.0}})
	if withDup == withoutDup {
		t.Errorf("duplicate point should add a zero-delta segment; encodings should differ")
	}
}

func TestEncodePolyline_Empty(t *testing.T) {
	if got := EncodePolyline(nil); got != "" {
		t.Errorf("EncodePolyline(nil) = %q, want empty string", got)
	}
}
