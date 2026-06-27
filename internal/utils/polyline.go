package utils

import "math"

// EncodePolyline encodes an ordered sequence of [lat, lon] coordinate pairs into a
// Google Encoded Polyline string.
//
// It deliberately mirrors the OneBusAway Java reference implementation
// (org.onebusaway.geospatial.services.PolylineEncoder), which floors coordinates with
// floor(coord * 1e5) rather than rounding. Matching the floor behaviour keeps maglev's
// `points` output byte-for-byte identical to the Java server. All points are included;
// no simplification or consecutive-duplicate filtering is performed.
func EncodePolyline(coords [][]float64) string {
	var b []byte
	var plat, plng int
	for _, c := range coords {
		late5 := floor1e5(c[0])
		lnge5 := floor1e5(c[1])
		b = encodeSignedNumber(b, late5-plat)
		b = encodeSignedNumber(b, lnge5-plng)
		plat = late5
		plng = lnge5
	}
	return string(b)
}

func floor1e5(coordinate float64) int {
	return int(math.Floor(coordinate * 1e5))
}

func encodeSignedNumber(b []byte, num int) []byte {
	sgnNum := num << 1
	if num < 0 {
		sgnNum = ^sgnNum
	}
	return encodeNumber(b, sgnNum)
}

func encodeNumber(b []byte, num int) []byte {
	for num >= 0x20 {
		b = append(b, byte((0x20|(num&0x1f))+63))
		num >>= 5
	}
	return append(b, byte(num+63))
}
