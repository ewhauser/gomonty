package vfs

import (
	"context"
	"errors"
	stdfs "io/fs"
	"path"
	"sort"
	"strings"
	"sync"
	"syscall"
	"time"
	"unicode/utf8"

	"github.com/ewhauser/gomonty"
)

// FileSystem is the typed OS surface expected by the Monty Go bindings.
type FileSystem interface {
	Exists(path string) (bool, error)
	IsFile(path string) (bool, error)
	IsDir(path string) (bool, error)
	IsSymlink(path string) (bool, error)
	ReadText(path string) (string, error)
	ReadBytes(path string) ([]byte, error)
	WriteText(path string, data string) (int, error)
	WriteBytes(path string, data []byte) (int, error)
	Mkdir(path string, parents bool, existOK bool) error
	Unlink(path string) error
	Rmdir(path string) error
	Iterdir(path string) ([]string, error)
	Stat(path string) (monty.StatResult, error)
	Rename(oldPath string, newPath string) error
	Resolve(path string) (string, error)
	Absolute(path string) (string, error)
}

// Environment is the typed environment-variable surface expected by the bindings.
type Environment interface {
	Get(key string) (string, bool)
	All() map[string]string
}

// MapEnvironment is a simple map-backed Environment.
type MapEnvironment map[string]string

// Get returns a single environment variable.
func (e MapEnvironment) Get(key string) (string, bool) {
	value, ok := e[key]
	return value, ok
}

// All returns a cloned environment map.
func (e MapEnvironment) All() map[string]string {
	cloned := make(map[string]string, len(e))
	for key, value := range e {
		cloned[key] = value
	}
	return cloned
}

// Handler converts a FileSystem and Environment into a Monty OS handler.
func Handler(fileSystem FileSystem, environment Environment) monty.OSHandler {
	return func(_ context.Context, call monty.OSCall) (monty.Result, error) {
		if strings.HasPrefix(string(call.Function), "Path.") && fileSystem == nil {
			message := "path operation called but no file system handler was provided"
			return monty.Raise(monty.Exception{Type: "NotImplementedError", Arg: &message}), nil
		}
		switch call.Function {
		case monty.OSPathExists:
			pathValue, err := pathArg(call.Args, 0)
			if err != nil {
				return monty.Raise(mapError(err)), nil
			}
			exists, err := fileSystem.Exists(pathValue)
			if err != nil {
				return monty.Raise(mapError(err)), nil
			}
			return monty.Return(monty.Bool(exists)), nil
		case monty.OSPathIsFile:
			pathValue, err := pathArg(call.Args, 0)
			if err != nil {
				return monty.Raise(mapError(err)), nil
			}
			isFile, err := fileSystem.IsFile(pathValue)
			if err != nil {
				return monty.Raise(mapError(err)), nil
			}
			return monty.Return(monty.Bool(isFile)), nil
		case monty.OSPathIsDir:
			pathValue, err := pathArg(call.Args, 0)
			if err != nil {
				return monty.Raise(mapError(err)), nil
			}
			isDir, err := fileSystem.IsDir(pathValue)
			if err != nil {
				return monty.Raise(mapError(err)), nil
			}
			return monty.Return(monty.Bool(isDir)), nil
		case monty.OSPathIsSymlink:
			pathValue, err := pathArg(call.Args, 0)
			if err != nil {
				return monty.Raise(mapError(err)), nil
			}
			isSymlink, err := fileSystem.IsSymlink(pathValue)
			if err != nil {
				return monty.Raise(mapError(err)), nil
			}
			return monty.Return(monty.Bool(isSymlink)), nil
		case monty.OSPathReadText:
			pathValue, err := pathArg(call.Args, 0)
			if err != nil {
				return monty.Raise(mapError(err)), nil
			}
			text, err := fileSystem.ReadText(pathValue)
			if err != nil {
				return monty.Raise(mapError(err)), nil
			}
			return monty.Return(monty.String(text)), nil
		case monty.OSPathReadBytes:
			pathValue, err := pathArg(call.Args, 0)
			if err != nil {
				return monty.Raise(mapError(err)), nil
			}
			data, err := fileSystem.ReadBytes(pathValue)
			if err != nil {
				return monty.Raise(mapError(err)), nil
			}
			return monty.Return(monty.Bytes(data)), nil
		case monty.OSPathWriteText:
			pathValue, err := pathArg(call.Args, 0)
			if err != nil {
				return monty.Raise(mapError(err)), nil
			}
			text, err := stringArg(call.Args, 1)
			if err != nil {
				return monty.Raise(mapError(err)), nil
			}
			written, err := fileSystem.WriteText(pathValue, text)
			if err != nil {
				return monty.Raise(mapError(err)), nil
			}
			return monty.Return(monty.Int(int64(written))), nil
		case monty.OSPathWriteBytes:
			pathValue, err := pathArg(call.Args, 0)
			if err != nil {
				return monty.Raise(mapError(err)), nil
			}
			data, err := bytesArg(call.Args, 1)
			if err != nil {
				return monty.Raise(mapError(err)), nil
			}
			written, err := fileSystem.WriteBytes(pathValue, data)
			if err != nil {
				return monty.Raise(mapError(err)), nil
			}
			return monty.Return(monty.Int(int64(written))), nil
		case monty.OSPathMkdir:
			pathValue, err := pathArg(call.Args, 0)
			if err != nil {
				return monty.Raise(mapError(err)), nil
			}
			parents, err := boolKwarg(call.Kwargs, "parents", false)
			if err != nil {
				return monty.Raise(mapError(err)), nil
			}
			existOK, err := boolKwarg(call.Kwargs, "exist_ok", false)
			if err != nil {
				return monty.Raise(mapError(err)), nil
			}
			if err := fileSystem.Mkdir(pathValue, parents, existOK); err != nil {
				return monty.Raise(mapError(err)), nil
			}
			return monty.Return(monty.None()), nil
		case monty.OSPathUnlink:
			pathValue, err := pathArg(call.Args, 0)
			if err != nil {
				return monty.Raise(mapError(err)), nil
			}
			if err := fileSystem.Unlink(pathValue); err != nil {
				return monty.Raise(mapError(err)), nil
			}
			return monty.Return(monty.None()), nil
		case monty.OSPathRmdir:
			pathValue, err := pathArg(call.Args, 0)
			if err != nil {
				return monty.Raise(mapError(err)), nil
			}
			if err := fileSystem.Rmdir(pathValue); err != nil {
				return monty.Raise(mapError(err)), nil
			}
			return monty.Return(monty.None()), nil
		case monty.OSPathIterdir:
			pathValue, err := pathArg(call.Args, 0)
			if err != nil {
				return monty.Raise(mapError(err)), nil
			}
			entries, err := fileSystem.Iterdir(pathValue)
			if err != nil {
				return monty.Raise(mapError(err)), nil
			}
			values := make([]monty.Value, 0, len(entries))
			for _, entry := range entries {
				values = append(values, monty.PathValue(monty.Path(entry)))
			}
			return monty.Return(monty.List(values...)), nil
		case monty.OSPathStat:
			pathValue, err := pathArg(call.Args, 0)
			if err != nil {
				return monty.Raise(mapError(err)), nil
			}
			stat, err := fileSystem.Stat(pathValue)
			if err != nil {
				return monty.Raise(mapError(err)), nil
			}
			value, err := monty.ValueOf(stat)
			if err != nil {
				return monty.Raise(mapError(err)), nil
			}
			return monty.Return(value), nil
		case monty.OSPathRename:
			oldPath, err := pathArg(call.Args, 0)
			if err != nil {
				return monty.Raise(mapError(err)), nil
			}
			newPath, err := pathArg(call.Args, 1)
			if err != nil {
				return monty.Raise(mapError(err)), nil
			}
			if err := fileSystem.Rename(oldPath, newPath); err != nil {
				return monty.Raise(mapError(err)), nil
			}
			return monty.Return(monty.None()), nil
		case monty.OSPathResolve:
			pathValue, err := pathArg(call.Args, 0)
			if err != nil {
				return monty.Raise(mapError(err)), nil
			}
			resolved, err := fileSystem.Resolve(pathValue)
			if err != nil {
				return monty.Raise(mapError(err)), nil
			}
			return monty.Return(monty.String(resolved)), nil
		case monty.OSPathAbsolute:
			pathValue, err := pathArg(call.Args, 0)
			if err != nil {
				return monty.Raise(mapError(err)), nil
			}
			absolute, err := fileSystem.Absolute(pathValue)
			if err != nil {
				return monty.Raise(mapError(err)), nil
			}
			return monty.Return(monty.String(absolute)), nil
		case monty.OSGetenv:
			key, err := stringArg(call.Args, 0)
			if err != nil {
				return monty.Raise(mapError(err)), nil
			}
			defaultValue := monty.None()
			if len(call.Args) > 1 {
				defaultValue = call.Args[1]
			}
			if environment == nil {
				return monty.Return(defaultValue), nil
			}
			value, ok := environment.Get(key)
			if !ok {
				return monty.Return(defaultValue), nil
			}
			return monty.Return(monty.String(value)), nil
		case monty.OSGetEnviron:
			items := monty.Dict{}
			if environment != nil {
				for key, value := range environment.All() {
					items = append(items, monty.Pair{
						Key:   monty.String(key),
						Value: monty.String(value),
					})
				}
			}
			return monty.Return(monty.DictValue(items)), nil
		default:
			message := "unsupported OS function"
			return monty.Raise(monty.Exception{Type: "NotImplementedError", Arg: &message}), nil
		}
	}
}

type entryKind uint8

const (
	entryKindDirectory entryKind = iota
	entryKindFile
	entryKindSymlink
)

type entry struct {
	kind   entryKind
	data   []byte
	target string
	stat   monty.StatResult
}

// MemoryFS is a simple in-memory filesystem implementation for Monty.
type MemoryFS struct {
	mu      sync.RWMutex
	entries map[string]*entry
}

// NewMemoryFS constructs an empty in-memory filesystem rooted at `/`.
func NewMemoryFS() *MemoryFS {
	fs := &MemoryFS{
		entries: map[string]*entry{
			"/": {
				kind: entryKindDirectory,
				stat: DirStat(0o755, time.Now()),
			},
		},
	}
	return fs
}

// AddText preloads a text file, creating parent directories as needed.
func (fs *MemoryFS) AddText(pathValue string, content string) {
	fs.mu.Lock()
	defer fs.mu.Unlock()
	normalized := normalizePath(pathValue)
	fs.ensureParents(normalized)
	fs.entries[normalized] = &entry{
		kind: entryKindFile,
		data: []byte(content),
		stat: FileStat(int64(len(content)), 0o644, time.Now()),
	}
}

// AddBytes preloads a binary file, creating parent directories as needed.
func (fs *MemoryFS) AddBytes(pathValue string, content []byte) {
	fs.mu.Lock()
	defer fs.mu.Unlock()
	normalized := normalizePath(pathValue)
	fs.ensureParents(normalized)
	fs.entries[normalized] = &entry{
		kind: entryKindFile,
		data: append([]byte(nil), content...),
		stat: FileStat(int64(len(content)), 0o644, time.Now()),
	}
}

// AddSymlink preloads a symlink entry.
func (fs *MemoryFS) AddSymlink(pathValue string, target string) {
	fs.mu.Lock()
	defer fs.mu.Unlock()
	normalized := normalizePath(pathValue)
	fs.ensureParents(normalized)
	fs.entries[normalized] = &entry{
		kind:   entryKindSymlink,
		target: normalizePath(target),
		stat:   SymlinkStat(0o777, time.Now()),
	}
}

// Exists reports whether the path exists.
func (fs *MemoryFS) Exists(pathValue string) (bool, error) {
	fs.mu.RLock()
	defer fs.mu.RUnlock()
	_, ok := fs.entries[normalizePath(pathValue)]
	return ok, nil
}

// IsFile reports whether the path is a file.
func (fs *MemoryFS) IsFile(pathValue string) (bool, error) {
	fs.mu.RLock()
	defer fs.mu.RUnlock()
	entry := fs.entries[normalizePath(pathValue)]
	return entry != nil && entry.kind == entryKindFile, nil
}

// IsDir reports whether the path is a directory.
func (fs *MemoryFS) IsDir(pathValue string) (bool, error) {
	fs.mu.RLock()
	defer fs.mu.RUnlock()
	entry := fs.entries[normalizePath(pathValue)]
	return entry != nil && entry.kind == entryKindDirectory, nil
}

// IsSymlink reports whether the path is a symlink.
func (fs *MemoryFS) IsSymlink(pathValue string) (bool, error) {
	fs.mu.RLock()
	defer fs.mu.RUnlock()
	entry := fs.entries[normalizePath(pathValue)]
	return entry != nil && entry.kind == entryKindSymlink, nil
}

// ReadText reads a UTF-8 file as text.
func (fs *MemoryFS) ReadText(pathValue string) (string, error) {
	fs.mu.RLock()
	defer fs.mu.RUnlock()
	entry, err := fs.requireFile(normalizePath(pathValue))
	if err != nil {
		return "", err
	}
	if !utf8.Valid(entry.data) {
		return "", &stdfs.PathError{Op: "read_text", Path: pathValue, Err: syscall.EILSEQ}
	}
	return string(entry.data), nil
}

// ReadBytes reads a file as raw bytes.
func (fs *MemoryFS) ReadBytes(pathValue string) ([]byte, error) {
	fs.mu.RLock()
	defer fs.mu.RUnlock()
	entry, err := fs.requireFile(normalizePath(pathValue))
	if err != nil {
		return nil, err
	}
	return append([]byte(nil), entry.data...), nil
}

// WriteText writes a UTF-8 text file.
func (fs *MemoryFS) WriteText(pathValue string, data string) (int, error) {
	return fs.writeFile(pathValue, []byte(data))
}

// WriteBytes writes a binary file.
func (fs *MemoryFS) WriteBytes(pathValue string, data []byte) (int, error) {
	return fs.writeFile(pathValue, data)
}

// Mkdir creates a directory.
func (fs *MemoryFS) Mkdir(pathValue string, parents bool, existOK bool) error {
	fs.mu.Lock()
	defer fs.mu.Unlock()
	normalized := normalizePath(pathValue)
	if normalized == "/" {
		return nil
	}

	if existing := fs.entries[normalized]; existing != nil {
		if existing.kind == entryKindDirectory && existOK {
			return nil
		}
		return &stdfs.PathError{Op: "mkdir", Path: pathValue, Err: stdfs.ErrExist}
	}

	parent := path.Dir(normalized)
	if !parents {
		parentEntry := fs.entries[parent]
		if parentEntry == nil {
			return &stdfs.PathError{Op: "mkdir", Path: pathValue, Err: stdfs.ErrNotExist}
		}
		if parentEntry.kind != entryKindDirectory {
			return &stdfs.PathError{Op: "mkdir", Path: parent, Err: syscall.ENOTDIR}
		}
	} else {
		fs.ensureParents(normalized)
	}

	fs.entries[normalized] = &entry{
		kind: entryKindDirectory,
		stat: DirStat(0o755, time.Now()),
	}
	return nil
}

// Unlink removes a file.
func (fs *MemoryFS) Unlink(pathValue string) error {
	fs.mu.Lock()
	defer fs.mu.Unlock()
	normalized := normalizePath(pathValue)
	entry := fs.entries[normalized]
	if entry == nil {
		return &stdfs.PathError{Op: "unlink", Path: pathValue, Err: stdfs.ErrNotExist}
	}
	if entry.kind == entryKindDirectory {
		return &stdfs.PathError{Op: "unlink", Path: pathValue, Err: syscall.EISDIR}
	}
	delete(fs.entries, normalized)
	return nil
}

// Rmdir removes an empty directory.
func (fs *MemoryFS) Rmdir(pathValue string) error {
	fs.mu.Lock()
	defer fs.mu.Unlock()
	normalized := normalizePath(pathValue)
	entry := fs.entries[normalized]
	if entry == nil {
		return &stdfs.PathError{Op: "rmdir", Path: pathValue, Err: stdfs.ErrNotExist}
	}
	if entry.kind != entryKindDirectory {
		return &stdfs.PathError{Op: "rmdir", Path: pathValue, Err: syscall.ENOTDIR}
	}
	for childPath := range fs.entries {
		if childPath != normalized && path.Dir(childPath) == normalized {
			return &stdfs.PathError{Op: "rmdir", Path: pathValue, Err: syscall.ENOTEMPTY}
		}
	}
	if normalized != "/" {
		delete(fs.entries, normalized)
	}
	return nil
}

// Iterdir lists the direct children of a directory.
func (fs *MemoryFS) Iterdir(pathValue string) ([]string, error) {
	fs.mu.RLock()
	defer fs.mu.RUnlock()
	normalized := normalizePath(pathValue)
	entry := fs.entries[normalized]
	if entry == nil {
		return nil, &stdfs.PathError{Op: "iterdir", Path: pathValue, Err: stdfs.ErrNotExist}
	}
	if entry.kind != entryKindDirectory {
		return nil, &stdfs.PathError{Op: "iterdir", Path: pathValue, Err: syscall.ENOTDIR}
	}
	children := make([]string, 0)
	for childPath := range fs.entries {
		if childPath != normalized && path.Dir(childPath) == normalized {
			children = append(children, childPath)
		}
	}
	sort.Strings(children)
	return children, nil
}

// Stat returns stat metadata for a path.
func (fs *MemoryFS) Stat(pathValue string) (monty.StatResult, error) {
	fs.mu.RLock()
	defer fs.mu.RUnlock()
	entry := fs.entries[normalizePath(pathValue)]
	if entry == nil {
		return monty.StatResult{}, &stdfs.PathError{Op: "stat", Path: pathValue, Err: stdfs.ErrNotExist}
	}
	return entry.stat, nil
}

// Rename renames a file or directory.
func (fs *MemoryFS) Rename(oldPath string, newPath string) error {
	fs.mu.Lock()
	defer fs.mu.Unlock()
	oldNormalized := normalizePath(oldPath)
	newNormalized := normalizePath(newPath)
	existingEntry := fs.entries[oldNormalized]
	if existingEntry == nil {
		return &stdfs.PathError{Op: "rename", Path: oldPath, Err: stdfs.ErrNotExist}
	}
	if _, exists := fs.entries[newNormalized]; exists {
		return &stdfs.PathError{Op: "rename", Path: newPath, Err: stdfs.ErrExist}
	}
	parent := path.Dir(newNormalized)
	parentEntry := fs.entries[parent]
	if parentEntry == nil {
		return &stdfs.PathError{Op: "rename", Path: parent, Err: stdfs.ErrNotExist}
	}
	if parentEntry.kind != entryKindDirectory {
		return &stdfs.PathError{Op: "rename", Path: parent, Err: syscall.ENOTDIR}
	}

	updates := make(map[string]*entry)
	for currentPath, currentEntry := range fs.entries {
		if currentPath == oldNormalized || strings.HasPrefix(currentPath, oldNormalized+"/") {
			suffix := strings.TrimPrefix(currentPath, oldNormalized)
			updates[newNormalized+suffix] = currentEntry
			delete(fs.entries, currentPath)
		}
	}
	for updatedPath, updatedEntry := range updates {
		fs.entries[updatedPath] = updatedEntry
	}
	return nil
}

// Resolve returns the absolute path, resolving symlink targets recursively.
func (fs *MemoryFS) Resolve(pathValue string) (string, error) {
	fs.mu.RLock()
	defer fs.mu.RUnlock()
	current := normalizePath(pathValue)
	seen := map[string]struct{}{}
	for {
		entry := fs.entries[current]
		if entry == nil {
			return current, nil
		}
		if entry.kind != entryKindSymlink {
			return current, nil
		}
		if _, ok := seen[current]; ok {
			return "", &stdfs.PathError{Op: "resolve", Path: pathValue, Err: syscall.ELOOP}
		}
		seen[current] = struct{}{}
		current = normalizePath(entry.target)
	}
}

// Absolute normalizes the path into Monty's absolute POSIX form.
func (fs *MemoryFS) Absolute(pathValue string) (string, error) {
	return normalizePath(pathValue), nil
}

// FileStat builds a file stat_result helper.
func FileStat(size int64, mode int64, modifiedAt time.Time) monty.StatResult {
	if mode < 0o1000 {
		mode |= 0o100_000
	}
	timestamp := float64(modifiedAt.UnixNano()) / float64(time.Second)
	return monty.StatResult{
		Mode:  mode,
		Ino:   0,
		Dev:   0,
		Nlink: 1,
		UID:   0,
		GID:   0,
		Size:  size,
		Atime: timestamp,
		Mtime: timestamp,
		Ctime: timestamp,
	}
}

// DirStat builds a directory stat_result helper.
func DirStat(mode int64, modifiedAt time.Time) monty.StatResult {
	if mode < 0o1000 {
		mode |= 0o040_000
	}
	timestamp := float64(modifiedAt.UnixNano()) / float64(time.Second)
	return monty.StatResult{
		Mode:  mode,
		Ino:   0,
		Dev:   0,
		Nlink: 2,
		UID:   0,
		GID:   0,
		Size:  4096,
		Atime: timestamp,
		Mtime: timestamp,
		Ctime: timestamp,
	}
}

// SymlinkStat builds a symlink stat_result helper.
func SymlinkStat(mode int64, modifiedAt time.Time) monty.StatResult {
	if mode < 0o1000 {
		mode |= 0o120_000
	}
	timestamp := float64(modifiedAt.UnixNano()) / float64(time.Second)
	return monty.StatResult{
		Mode:  mode,
		Ino:   0,
		Dev:   0,
		Nlink: 1,
		UID:   0,
		GID:   0,
		Size:  0,
		Atime: timestamp,
		Mtime: timestamp,
		Ctime: timestamp,
	}
}

func (fs *MemoryFS) writeFile(pathValue string, data []byte) (int, error) {
	fs.mu.Lock()
	defer fs.mu.Unlock()

	normalized := normalizePath(pathValue)
	parent := path.Dir(normalized)
	parentEntry := fs.entries[parent]
	if parentEntry == nil {
		return 0, &stdfs.PathError{Op: "write", Path: pathValue, Err: stdfs.ErrNotExist}
	}
	if parentEntry.kind != entryKindDirectory {
		return 0, &stdfs.PathError{Op: "write", Path: parent, Err: syscall.ENOTDIR}
	}

	if existing := fs.entries[normalized]; existing != nil && existing.kind == entryKindDirectory {
		return 0, &stdfs.PathError{Op: "write", Path: pathValue, Err: syscall.EISDIR}
	}

	fs.entries[normalized] = &entry{
		kind: entryKindFile,
		data: append([]byte(nil), data...),
		stat: FileStat(int64(len(data)), 0o644, time.Now()),
	}
	return len(data), nil
}

func (fs *MemoryFS) requireFile(pathValue string) (*entry, error) {
	entry := fs.entries[pathValue]
	if entry == nil {
		return nil, &stdfs.PathError{Op: "read", Path: pathValue, Err: stdfs.ErrNotExist}
	}
	if entry.kind == entryKindDirectory {
		return nil, &stdfs.PathError{Op: "read", Path: pathValue, Err: syscall.EISDIR}
	}
	return entry, nil
}

func (fs *MemoryFS) ensureParents(pathValue string) {
	parent := path.Dir(pathValue)
	if parent == "." || parent == "/" {
		return
	}
	if _, ok := fs.entries[parent]; ok {
		return
	}
	fs.ensureParents(parent)
	fs.entries[parent] = &entry{
		kind: entryKindDirectory,
		stat: DirStat(0o755, time.Now()),
	}
}

func normalizePath(pathValue string) string {
	if pathValue == "" {
		return "/"
	}
	if !strings.HasPrefix(pathValue, "/") {
		pathValue = "/" + pathValue
	}
	cleaned := path.Clean(pathValue)
	if cleaned == "." {
		return "/"
	}
	return cleaned
}

func pathArg(args []monty.Value, index int) (string, error) {
	if index >= len(args) {
		return "", errors.New("missing path argument")
	}
	value := args[index]
	switch value.Kind() {
	case "path":
		return string(value.Raw().(monty.Path)), nil
	case "string":
		return value.Raw().(string), nil
	default:
		return "", errors.New("path argument must be a path or string")
	}
}

func stringArg(args []monty.Value, index int) (string, error) {
	if index >= len(args) {
		return "", errors.New("missing string argument")
	}
	value := args[index]
	if value.Kind() != "string" {
		return "", errors.New("argument must be a string")
	}
	return value.Raw().(string), nil
}

func bytesArg(args []monty.Value, index int) ([]byte, error) {
	if index >= len(args) {
		return nil, errors.New("missing bytes argument")
	}
	value := args[index]
	if value.Kind() != "bytes" {
		return nil, errors.New("argument must be bytes")
	}
	return append([]byte(nil), value.Raw().([]byte)...), nil
}

func boolKwarg(kwargs monty.Dict, name string, defaultValue bool) (bool, error) {
	for _, pair := range kwargs {
		if pair.Key.Kind() != "string" {
			continue
		}
		if pair.Key.Raw().(string) != name {
			continue
		}
		if pair.Value.Kind() != "bool" {
			return false, errors.New("boolean keyword argument must be bool")
		}
		return pair.Value.Raw().(bool), nil
	}
	return defaultValue, nil
}

func mapError(err error) monty.Exception {
	switch {
	case err == nil:
		return monty.Exception{Type: "RuntimeError"}
	case errors.Is(err, stdfs.ErrNotExist):
		message := err.Error()
		return monty.Exception{Type: "FileNotFoundError", Arg: &message}
	case errors.Is(err, stdfs.ErrExist):
		message := err.Error()
		return monty.Exception{Type: "FileExistsError", Arg: &message}
	case errors.Is(err, syscall.EISDIR):
		message := err.Error()
		return monty.Exception{Type: "IsADirectoryError", Arg: &message}
	case errors.Is(err, syscall.ENOTDIR):
		message := err.Error()
		return monty.Exception{Type: "NotADirectoryError", Arg: &message}
	case errors.Is(err, syscall.EILSEQ):
		message := err.Error()
		return monty.Exception{Type: "UnicodeDecodeError", Arg: &message}
	default:
		message := err.Error()
		return monty.Exception{Type: "OSError", Arg: &message}
	}
}
