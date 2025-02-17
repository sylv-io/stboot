// Copyright 2021 the System Transparency Authors. All rights reserved
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

import (
	"bytes"
	"fmt"
	"io"
	"io/ioutil"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
	"unicode/utf16"
	"unicode/utf8"

	"github.com/rekby/gpt"
	"github.com/u-root/u-root/pkg/mount"
)

const (
	dataPartitionFSType     = "ext4"
	dataPartitionLabel      = "STDATA"
	dataPartitionMountPoint = "data"
	bootPartitionFSType     = "vfat"
	bootPartitionLabel      = "STBOOT"
	bootPartitionMountPoint = "boot"
)

func mountBootPartition() error {
	return findPartition(bootPartitionLabel, bootPartitionFSType, bootPartitionMountPoint, 60)
}

func mountDataPartition() error {
	return findPartition(dataPartitionLabel, dataPartitionFSType, dataPartitionMountPoint, 60)
}

func findPartition(label, fsType, mountPoint string, timeout uint) error {
	debug("Search partition with label %s ...", label)
	fs, err := ioutil.ReadFile("/proc/filesystems")
	if err != nil {
		return err
	}
	if !strings.Contains(string(fs), fsType) {
		return fmt.Errorf("filesystem unknown: %s", fsType)
	}

	var devices []string
	for {
		devices, err = getBlockDevs()
		if err != nil {
			return fmt.Errorf("getting block devices failed with: %v", err)
		}
		if len(devices) == 0 {
			if timeout == 0 {
				return fmt.Errorf("no non-loopback block devices found")
			}
			debug("waiting for block devices to appear %d...", timeout)

			timeout--
			time.Sleep(time.Second)
		} else {
			break
		}
	}

	device, err := deviceByPartLabel(devices, label)
	if err != nil {
		return fmt.Errorf("failed to get device with label %s: %v", label, err)
	}

	mp, err := mount.Mount(device, mountPoint, fsType, "", 0)
	if err != nil {
		return fmt.Errorf("failed to mount device %s: %v", device, err)
	}

	debug("partition %s mounted at %s", mp.Device, mp.Path)
	return nil
}

func getBlockDevs() ([]string, error) {
	devnames := make([]string, 0)
	root := "/sys/class/block"
	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		if rel == "." {
			return nil
		}
		if strings.Contains(rel, "loop") {
			return nil
		}
		dev := filepath.Join("/dev", rel)
		devnames = append(devnames, dev)
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("Walking %s returned an error: %v", root, err)
	}
	return devnames, nil
}

func deviceByPartLabel(devices []string, label string) (string, error) {
	var d string
	var p string
	for _, device := range devices {
		fd, err := os.Open(device)
		if err != nil {
			debug("Skip %s: %v", device, err)
			continue
		}
		defer fd.Close()
		if _, err = fd.Seek(512, io.SeekStart); err != nil {
			debug("Skip %s: %v", device, err)
			continue
		}
		table, err := gpt.ReadTable(fd, 512)
		if err != nil {
			debug("Skip %s: %v", device, err)
			continue
		}
		for n, part := range table.Partitions {
			if part.IsEmpty() {
				debug("Skip %s: no partitions found", device)
				continue
			}
			l, err := decodeLabel(part.PartNameUTF16[:])
			if err != nil {
				debug("Skip %s partition %d: %v", device, n+1, err)
				continue
			}
			if l == label {
				d = device
				p = strconv.Itoa(n + 1)
				info("Found partition on %s , partition %s", device, p)
				break
			}
			debug("Skip %s partition %d: label does not match %s", device, n+1, label)
		}
		if d != "" && p != "" {
			break
		}
	}
	if d != "" && p != "" {
		for _, device := range devices {
			if !strings.HasPrefix(device, d) {
				continue
			}
			part := strings.TrimPrefix(device, d)
			if strings.Contains(part, p) {
				return device, nil
			}
		}
		return "", fmt.Errorf("Cannot find partition %s of %s in %v", p, d, devices)
	}
	return "", fmt.Errorf("No device with partition labeled %s found", label)
}

func decodeLabel(b []byte) (string, error) {

	if len(b)%2 != 0 {
		return "", fmt.Errorf("label has odd number of bytes")
	}

	u16s := make([]uint16, 1)
	ret := &bytes.Buffer{}
	b8buf := make([]byte, 4)

	lb := len(b)
	for i := 0; i < lb; i += 2 {
		u16s[0] = uint16(b[i]) + (uint16(b[i+1]) << 8)
		r := utf16.Decode(u16s)
		n := utf8.EncodeRune(b8buf, r[0])
		ret.Write(b8buf[:n])
	}

	return strings.Trim(ret.String(), "\x00"), nil
}
