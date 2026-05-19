//go:build !linux

package runtime

func SdNotify(_ string) error {
	return nil
}
