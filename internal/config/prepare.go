package config

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"syscall"

	"github.com/vatebur/gamehelm/internal/auth"
)

// Prepare keeps a separate lock inode alive while GameHelm uses this config.
// Call the returned release function when the application exits.
func Prepare(path string) (Config, func(), error) {
	path, err := filepath.Abs(path)
	if err != nil {
		return Config{}, nil, errors.New("无法定位配置文件")
	}
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() {
		return Config{}, nil, errors.New("配置路径必须指向普通文件")
	}
	lock, err := os.OpenFile(path+".lock", os.O_CREATE|os.O_RDWR|syscall.O_NOFOLLOW, 0o600)
	if err != nil {
		return Config{}, nil, errors.New("无法创建配置实例锁，请确认配置目录可写")
	}
	if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		_ = lock.Close()
		return Config{}, nil, errors.New("另一 GameHelm 实例正在使用此配置")
	}
	release := func() {
		_ = syscall.Flock(int(lock.Fd()), syscall.LOCK_UN)
		_ = lock.Close()
	}
	cfg, err := prepareLocked(path)
	if err != nil {
		release()
		return Config{}, nil, err
	}
	return cfg, release, nil
}

func prepareLocked(path string) (Config, error) {
	original, err := readConfig(path)
	if err != nil {
		return Config{}, err
	}
	cfg, err := decodeConfig(original)
	if err != nil {
		return Config{}, err
	}
	if cfg.Auth.Password == "" {
		if err := os.Chmod(path, 0o600); err != nil {
			return Config{}, errors.New("设置配置权限失败")
		}
		return cfg, nil
	}
	hash, err := auth.HashPassword(cfg.Auth.Password)
	if err != nil {
		return Config{}, err
	}
	cfg.Auth.Password = ""
	cfg.Auth.PasswordHash = hash
	data, err := rewritePassword(original, hash)
	if err != nil {
		return Config{}, err
	}
	if err := replaceConfig(path, original, data); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

func replaceConfig(path string, original, data []byte) error {
	directory := filepath.Dir(path)
	temporary, err := os.CreateTemp(directory, ".gamehelm-config-*")
	if err != nil {
		return errors.New("创建配置临时文件失败，请确认配置目录可写")
	}
	temporaryPath := temporary.Name()
	defer func() {
		_ = temporary.Close()
		_ = os.Remove(temporaryPath)
	}()
	if err := temporary.Chmod(0o600); err != nil {
		return errors.New("设置配置临时文件权限失败")
	}
	if _, err := temporary.Write(data); err != nil {
		return errors.New("写入哈希配置失败")
	}
	if err := temporary.Sync(); err != nil {
		return errors.New("同步哈希配置失败")
	}
	if err := temporary.Close(); err != nil {
		return errors.New("关闭哈希配置临时文件失败")
	}
	current, err := readConfig(path)
	if err != nil || !bytes.Equal(current, original) {
		return errors.New("配置在启动过程中发生变化，已取消回写")
	}
	if err := os.Rename(temporaryPath, path); err != nil {
		return errors.New("原子替换哈希配置失败")
	}
	dir, err := os.Open(directory)
	if err != nil {
		return errors.New("打开配置目录以同步失败")
	}
	defer func() { _ = dir.Close() }()
	if err := dir.Sync(); err != nil {
		return fmt.Errorf("同步配置目录失败: %w", err)
	}
	return nil
}
