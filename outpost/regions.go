package main

import (
	"encoding/binary"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
)

// A world's terrain and what was built on it, beside world.json: world.regions, a header and then one record of
// SaveChunkKB for each terrain chunk (SaveBaseMB of them) and each explored one. Like a real game's region files it is
// rewritten in place, only the chunks that changed since the last save (a new or damaged file is written whole), so
// a save of a few hundred MB costs a few MB of writes and a snapshot of it sends only what changed.
const regionsFile = "world.regions"

const regionMagic = "OUTPOSTR"

type regionHeader struct {
	Magic   [8]byte
	Format  uint32
	Record  uint32 // bytes a record
	Records uint64
	Seed    uint64
	_       [32]byte
}

const regionHeaderSize = 64

func (l Load) recordBytes() int { return l.SaveChunkKB << 10 }

// terrainRecords is how many records the world's terrain takes before any exploring.
func (l Load) terrainRecords() int {
	if l.SaveChunkKB <= 0 {
		return 0
	}
	return l.SaveBaseMB << 10 / l.SaveChunkKB
}

func (g *game) regionsPath() string { return filepath.Join(g.cfg.SaveDir, regionsFile) }

// records is how many records the world's file has now.
func (g *game) records() int { return g.load.terrainRecords() + g.save.Chunks }

// touchAt marks the terrain chunk under a position as changed (something was gathered or built there).
func (g *game) touchAt(p vec) {
	n := g.load.terrainRecords()
	if n == 0 {
		return
	}
	side := 1
	for side*side < n {
		side++
	}
	cx := min(int(p.x/g.size*float64(side)), side-1)
	cy := min(int(p.y/g.size*float64(side)), side-1)
	g.dirty[min(cy*side+cx, n-1)] = true
}

// writeRegions writes the changed and new records, or the whole file when it is missing or not this world's shape,
// and returns the file's size.
func (g *game) writeRegions() (int64, error) {
	rec := g.load.recordBytes()
	total := g.records()
	want := int64(regionHeaderSize) + int64(total)*int64(rec)
	path := g.regionsPath()
	hdr := regionHeader{Format: 1, Record: uint32(rec), Records: uint64(total), Seed: g.seed}
	copy(hdr.Magic[:], regionMagic)

	have, whole := 0, true
	if f, err := os.Open(path); err == nil {
		var old regionHeader
		st, serr := f.Stat()
		if binary.Read(f, binary.LittleEndian, &old) == nil && serr == nil && string(old.Magic[:]) == regionMagic &&
			old.Record == uint32(rec) && old.Seed == g.seed && int(old.Records) <= total &&
			st.Size() == int64(regionHeaderSize)+int64(old.Records)*int64(rec) {
			have, whole = int(old.Records), false
		}
		f.Close()
	}
	buf := make([]byte, rec)
	if whole {
		tmp := path + ".new"
		f, err := os.OpenFile(tmp, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o644)
		if err != nil {
			return 0, err
		}
		err = binary.Write(f, binary.LittleEndian, hdr)
		for i := 0; i < total && err == nil; i++ {
			g.record(buf, i)
			_, err = f.Write(buf)
		}
		if cerr := f.Close(); err == nil {
			err = cerr
		}
		if err == nil {
			err = os.Rename(tmp, path)
		}
		if err != nil {
			os.Remove(tmp)
			return 0, err
		}
		clear(g.dirty)
		return want, nil
	}
	f, err := os.OpenFile(path, os.O_WRONLY, 0o644)
	if err != nil {
		return 0, err
	}
	defer f.Close()
	idx := make([]int, 0, len(g.dirty)+total-have)
	for i := range g.dirty {
		if i < have {
			idx = append(idx, i)
		}
	}
	sort.Ints(idx)
	for i := have; i < total; i++ {
		idx = append(idx, i)
	}
	for _, i := range idx {
		g.record(buf, i)
		if _, err := f.WriteAt(buf, int64(regionHeaderSize)+int64(i)*int64(rec)); err != nil {
			return 0, err
		}
	}
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		return 0, err
	}
	if err := binary.Write(f, binary.LittleEndian, hdr); err != nil {
		return 0, err
	}
	clear(g.dirty)
	return want, nil
}

// record fills buf with chunk i as it is now: its number and revision, block types (runs, as terrain is) and
// per-block state (varied). It compresses about as well as a real game's chunk data does.
func (g *game) record(buf []byte, i int) {
	g.revision++
	binary.LittleEndian.PutUint64(buf[0:], uint64(i))
	binary.LittleEndian.PutUint64(buf[8:], g.revision)
	x := g.seed ^ uint64(i)*0x9E3779B97F4A7C15 ^ g.revision<<32 | 1
	next := func() uint64 { x ^= x << 13; x ^= x >> 7; x ^= x << 17; return x }
	half := len(buf) / 2
	for p := 16; p < half; {
		v := next()
		run := 8 + int(v>>58)
		b := byte(v >> 8 & 0x1f)
		for k := 0; k < run && p < half; k++ {
			buf[p] = b
			p++
		}
	}
	for p := half; p+8 <= len(buf); p += 8 {
		binary.LittleEndian.PutUint64(buf[p:], next())
	}
}

// size says a number of bytes the way the panel does.
func size(b int64) string {
	switch {
	case b >= 10<<30:
		return fmt.Sprintf("%d GB", b>>30)
	case b >= 1<<30:
		return fmt.Sprintf("%.1f GB", float64(b)/(1<<30))
	case b >= 1<<20:
		return fmt.Sprintf("%d MB", b>>20)
	case b >= 1<<10:
		return fmt.Sprintf("%d KB", b>>10)
	}
	return fmt.Sprintf("%d bytes", b)
}
