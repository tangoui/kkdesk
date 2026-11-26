package appui

import (
	"fmt"

	"github.com/tangoui/tango/ui"
	"github.com/tangoui/tango/ui/layout"
)

// confirmFooter is the footer of a request dialog that times out: the
// countdown as its own text, then the buttons. The countdown must not be
// part of a button's label: a widget's identity comes from its label, so
// the button would become another one every second and a click whose press
// and release straddle a tick was lost (several clicks to accept).
func confirmFooter(c *ui.Context, rejectLabel, acceptLabel, timeoutAction string, left int) (reject, accept bool) {
	ui.Row(c, func() {
		ui.Text(c, fmt.Sprintf("%d 秒后%s", left, timeoutAction)).Size(12).Tertiary().NoWrap()
		ui.Spacer(c)
		reject = ui.Button(c, rejectLabel).AutoInsertSpace(false).Clicked()
		accept = ui.Button(c, acceptLabel).AutoInsertSpace(false).Primary().Clicked()
	}, layout.Gap(8), layout.Middle())
	return reject, accept
}
