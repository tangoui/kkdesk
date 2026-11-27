package capture

// dxgiNotFound is DXGI_ERROR_NOT_FOUND (0x887A0002). Opening the duplication
// returns it when the primary monitor is not among the device adapter's
// outputs: after a display change (a monitor plugged in, a virtual display
// becoming primary) DXGI objects created before still list the old outputs.
const dxgiNotFound = int32(-0x7785FFFE)

// openReopening runs open; on dxgiNotFound it resets the device (so the next
// open gets a current one) and tries once more.
func openReopening(open func() int32, reset func()) int32 {
	hr := open()
	if hr == dxgiNotFound {
		reset()
		hr = open()
	}
	return hr
}
