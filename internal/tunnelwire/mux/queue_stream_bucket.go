package mux

import "sync"

// streamQuantum is how many frames a stream sends per turn. Single frame turns
// made every reader wake per frame and cost about a quarter of bulk throughput
// on loopback; 8 frames (about 32 KiB) restore it.
const streamQuantum = 8

// streamBucketSet holds per-stream frame FIFOs. Streams that were idle go first,
// so a small stream waits for one frame, not for a turn of each bulk stream;
// backlogged streams then share the rest round robin. Its owner provides locking.
type streamBucketSet struct {
	buckets map[int32]*streamBucket
	fresh   bucketList // streams that had nothing queued
	backlog bucketList // streams that used a full turn and still have frames
}

type streamBucket struct {
	id   int32
	turn uint8 // frames sent in the current turn
	head *muxPackager
	tail *muxPackager
	next *streamBucket
}

type bucketList struct {
	head *streamBucket
	tail *streamBucket
}

func (l *bucketList) append(bucket *streamBucket) {
	if l.tail == nil {
		l.head = bucket
	} else {
		l.tail.next = bucket
	}
	l.tail = bucket
}

func (l *bucketList) dropHead() {
	bucket := l.head
	l.head = bucket.next
	if l.head == nil {
		l.tail = nil
	}
	bucket.next = nil
}

var streamBucketPool = sync.Pool{
	New: func() any { return new(streamBucket) },
}

func (s *streamBucketSet) init() {
	s.buckets = make(map[int32]*streamBucket)
}

func (s *streamBucketSet) hasPending() bool {
	return s.fresh.head != nil || s.backlog.head != nil
}

func (s *streamBucketSet) push(packager *muxPackager) {
	packager.queueNext = nil
	bucket := s.buckets[packager.id]
	if bucket == nil {
		bucket = streamBucketPool.Get().(*streamBucket)
		bucket.id = packager.id
		bucket.head = packager
		bucket.tail = packager
		s.buckets[packager.id] = bucket
		s.fresh.append(bucket)
		return
	}
	bucket.tail.queueNext = packager
	bucket.tail = packager
}

func (s *streamBucketSet) pop() *muxPackager {
	list := &s.fresh
	if list.head == nil {
		list = &s.backlog
	}
	bucket := list.head
	if bucket == nil {
		return nil
	}
	packager := bucket.head
	bucket.head = packager.queueNext
	packager.queueNext = nil
	bucket.turn++
	switch {
	case bucket.head == nil:
		list.dropHead()
		delete(s.buckets, bucket.id)
		*bucket = streamBucket{}
		streamBucketPool.Put(bucket)
	case bucket.turn >= streamQuantum:
		list.dropHead()
		bucket.turn = 0
		s.backlog.append(bucket)
	}
	return packager
}
