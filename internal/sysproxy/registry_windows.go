package sysproxy

import (
	"errors"
	"fmt"
	"math"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
)

// internetSettingsPath is the per-user WinINet proxy key.
const internetSettingsPath = `Software\Microsoft\Windows\CurrentVersion\Internet Settings`

// windowsRegistry and internetSetOption talk to the real machine.
// Tests build a Client with a memory registry and must not call either one.
type windowsRegistry struct{}

func withInternetSettings(fn func(registry.Key) error) error {
	k, err := registry.OpenKey(registry.CURRENT_USER, internetSettingsPath, registry.QUERY_VALUE|registry.SET_VALUE)
	if err != nil {
		return err
	}
	defer k.Close()
	return fn(k)
}

func (windowsRegistry) GetString(name string) (string, bool, error) {
	var (
		value  string
		exists bool
	)
	err := withInternetSettings(func(k registry.Key) error {
		v, _, err := k.GetStringValue(name)
		if errors.Is(err, registry.ErrNotExist) {
			return nil
		}
		if err != nil {
			return err
		}
		value = v
		exists = true
		return nil
	})
	if err != nil {
		return "", false, fmt.Errorf("读取 %s 失败: %w", name, err)
	}
	return value, exists, nil
}

func (windowsRegistry) GetDWord(name string) (uint32, bool, error) {
	var (
		value  uint32
		exists bool
	)
	err := withInternetSettings(func(k registry.Key) error {
		v, _, err := k.GetIntegerValue(name)
		if errors.Is(err, registry.ErrNotExist) {
			return nil
		}
		if err != nil {
			return err
		}
		if v > math.MaxUint32 {
			return fmt.Errorf("%s 超出 DWORD 范围", name)
		}
		value = uint32(v)
		exists = true
		return nil
	})
	if err != nil {
		return 0, false, fmt.Errorf("读取 %s 失败: %w", name, err)
	}
	return value, exists, nil
}

func (windowsRegistry) SetString(name, value string) error {
	err := withInternetSettings(func(k registry.Key) error {
		return k.SetStringValue(name, value)
	})
	if err != nil {
		return fmt.Errorf("写入 %s 失败: %w", name, err)
	}
	return nil
}

func (windowsRegistry) SetDWord(name string, value uint32) error {
	err := withInternetSettings(func(k registry.Key) error {
		return k.SetDWordValue(name, value)
	})
	if err != nil {
		return fmt.Errorf("写入 %s 失败: %w", name, err)
	}
	return nil
}

func (windowsRegistry) DeleteValue(name string) error {
	err := withInternetSettings(func(k registry.Key) error {
		err := k.DeleteValue(name)
		if errors.Is(err, registry.ErrNotExist) {
			return nil
		}
		return err
	})
	if err != nil {
		return fmt.Errorf("删除 %s 失败: %w", name, err)
	}
	return nil
}

func internetSetOption(option uintptr) error {
	proc := windows.NewLazySystemDLL("wininet.dll").NewProc("InternetSetOptionW")
	r1, _, callErr := proc.Call(0, option, 0, 0)
	if r1 == 0 {
		if callErr != nil {
			return fmt.Errorf("InternetSetOptionW %d: %w", option, callErr)
		}
		return fmt.Errorf("InternetSetOptionW %d 失败", option)
	}
	return nil
}
