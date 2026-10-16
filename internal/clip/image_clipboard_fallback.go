//go:build !darwin && !windows

package clip

import "github.com/egoist/mygo/transfer"

func readClipboardImage() (Item, error) {
	raw, err := readClipboardData(transfer.PNG)
	if err != nil {
		return Item{}, err
	}
	return FromPNG(raw)
}
