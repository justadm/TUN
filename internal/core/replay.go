package core

// ReplayWindow tracks recently seen sequence numbers in a fixed-size window.
type ReplayWindow struct {
	windowSize uint64
	maxSeq     uint64
	bitmap     []uint64
}

// NewReplayWindow creates a replay window of given size (must be multiple of 64).
func NewReplayWindow(size uint64) *ReplayWindow {
	if size == 0 {
		size = 4096
	}
	if size%64 != 0 {
		// round up to next multiple of 64
		size = ((size / 64) + 1) * 64
	}
	return &ReplayWindow{
		windowSize: size,
		bitmap:     make([]uint64, size/64),
	}
}

// Accept returns true if seq is new and within acceptable window.
func (w *ReplayWindow) Accept(seq uint64) bool {
	if seq == 0 {
		return false
	}
	if w.maxSeq == 0 {
		w.maxSeq = seq
		w.set(seq)
		return true
	}

	if seq > w.maxSeq {
		delta := seq - w.maxSeq
		if delta >= w.windowSize {
			// reset window
			for i := range w.bitmap {
				w.bitmap[i] = 0
			}
		} else {
			w.shift(delta)
		}
		w.maxSeq = seq
		w.set(seq)
		return true
	}

	// seq <= maxSeq
	if w.maxSeq-seq >= w.windowSize {
		return false
	}
	if w.isSet(seq) {
		return false
	}
	w.set(seq)
	return true
}

func (w *ReplayWindow) bitIndex(seq uint64) (word int, bit uint) {
	offset := w.maxSeq - seq
	idx := offset % w.windowSize
	word = int(idx / 64)
	bit = uint(idx % 64)
	return
}

func (w *ReplayWindow) isSet(seq uint64) bool {
	word, bit := w.bitIndex(seq)
	return (w.bitmap[word]&(1<<bit)) != 0
}

func (w *ReplayWindow) set(seq uint64) {
	word, bit := w.bitIndex(seq)
	w.bitmap[word] |= 1 << bit
}

func (w *ReplayWindow) shift(delta uint64) {
	if delta == 0 {
		return
	}
	words := int(delta / 64)
	bits := uint(delta % 64)
	if words > 0 {
		copy(w.bitmap[words:], w.bitmap[:len(w.bitmap)-words])
		for i := 0; i < words; i++ {
			w.bitmap[i] = 0
		}
	}
	if bits > 0 {
		var carry uint64
		for i := 0; i < len(w.bitmap); i++ {
			newCarry := w.bitmap[i] >> (64 - bits)
			w.bitmap[i] = (w.bitmap[i] << bits) | carry
			carry = newCarry
		}
	}
}
