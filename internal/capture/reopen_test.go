package capture

import "testing"

// After a display change the device's adapter no longer lists the primary
// monitor (DXGI_ERROR_NOT_FOUND): the device is recreated and the open
// retried once.
func TestOpenRetriesOnceAfterDisplayChange(t *testing.T) {
	const notFound = int32(-0x7785FFFE) // 0x887A0002
	for _, tc := range []struct {
		name    string
		results []int32
		want    int32
		resets  int
		opens   int
	}{
		{"opens", []int32{0}, 0, 0, 1},
		{"display changed", []int32{notFound, 0}, 0, 1, 2},
		{"still missing", []int32{notFound, notFound}, notFound, 1, 2},
		{"other error", []int32{-0x7FFF0001}, -0x7FFF0001, 0, 1},
	} {
		opens, resets := 0, 0
		got := openReopening(func() int32 { opens++; return tc.results[opens-1] }, func() { resets++ })
		if got != tc.want || resets != tc.resets || opens != tc.opens {
			t.Errorf("%s: result %#x, %d resets, %d opens; want %#x, %d, %d", tc.name, got, resets, opens, tc.want, tc.resets, tc.opens)
		}
	}
}
