package capture

import "testing"

func TestScaledSize(t *testing.T) {
	cases := []struct {
		w, h, max    int
		wantW, wantH int
	}{
		{3840, 2160, 1920, 1920, 1080},
		{2560, 1600, 1920, 1920, 1200},
		{1366, 768, 1920, 1366, 768},
		{3024, 1964, 1920, 1920, 1246},
		{1365, 767, 1920, 1364, 766},
		{3840, 2160, 0, 3840, 2160},
		{1920, 1080, 1920, 1920, 1080},
	}
	for _, c := range cases {
		w, h := scaledSize(c.w, c.h, c.max)
		if w != c.wantW || h != c.wantH {
			t.Errorf("scaledSize(%d,%d,%d) = %dx%d, want %dx%d", c.w, c.h, c.max, w, h, c.wantW, c.wantH)
		}
		if w%2 != 0 || h%2 != 0 {
			t.Errorf("scaledSize(%d,%d,%d) = %dx%d not even", c.w, c.h, c.max, w, h)
		}
	}
}
