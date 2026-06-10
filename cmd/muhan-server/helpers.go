package main

import (
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"muhan/internal/world/model"
)

func serverRemoteHost(addr net.Addr) string {
	if addr == nil {
		return ""
	}
	raw := addr.String()
	host, _, err := net.SplitHostPort(raw)
	if err != nil {
		return raw
	}
	return host
}

func serverSafeRootPath(root, rel string) (string, bool) {
	root = strings.TrimSpace(root)
	rel = filepath.Clean(filepath.FromSlash(strings.TrimSpace(rel)))
	if root == "" || rel == "." || filepath.IsAbs(rel) || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", false
	}
	full := filepath.Join(root, rel)
	absRoot, rootErr := filepath.Abs(root)
	absFull, fullErr := filepath.Abs(full)
	if rootErr != nil || fullErr != nil {
		return "", false
	}
	if absFull == absRoot || strings.HasPrefix(absFull, absRoot+string(filepath.Separator)) {
		return full, true
	}
	return "", false
}

func serverRemoveFiles(paths []string) error {
	for _, path := range serverUniqueStrings(paths) {
		info, err := os.Lstat(path)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return err
		}
		if info.IsDir() {
			return fmt.Errorf("refusing to remove directory %q", path)
		}
		if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	return nil
}

func serverCreatureFlag(creature model.Creature, keys ...string) bool {
	for _, key := range keys {
		if creature.Stats != nil && creature.Stats[key] != 0 {
			return true
		}
		if creature.Properties != nil && serverTruthy(creature.Properties[key]) {
			return true
		}
		for _, tag := range creature.Metadata.Tags {
			if strings.EqualFold(tag, key) {
				return true
			}
		}
	}
	return false
}

func serverCreatureInt(creature model.Creature, keys ...string) (int, bool) {
	for _, key := range keys {
		if creature.Stats != nil {
			if value, ok := creature.Stats[key]; ok {
				return value, true
			}
		}
		if creature.Properties != nil {
			if value, err := strconv.Atoi(strings.TrimSpace(creature.Properties[key])); err == nil {
				return value, true
			}
		}
	}
	return 0, false
}

func serverTruthy(value string) bool {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "1", "true", "yes", "y", "on":
		return true
	default:
		return false
	}
}

func serverUniqueStrings(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	return out
}
