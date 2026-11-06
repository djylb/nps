package mux

import "sync"

// streamBucketSet holds per-stream frame FIFOs and serves the streams with
// pending frames round robin, one frame per turn. Its owner provides locking.
type streamBucketSet struct {
	buckets    map[int32]*streamBucket
	activeHead *streamBucket
	activeTail *streamBucket
}

type streamBucket struct {
	id   int32
	head *muxPackager
	tail *muxPackager
	next *streamBucket // next stream in the active list
}

var streamBucketPool = sync.Pool{
	New: func() any { return new(streamBucket) },
}

func (s *streamBucketSet) init() {
	s.buckets = make(map[int32]*streamBucket)
}

func (s *streamBucketSet) hasPending() bool {
	return s.activeHead != nil
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
		s.appendActive(bucket)
		return
	}
	bucket.tail.queueNext = packager
	bucket.tail = packager
}

func (s *streamBucketSet) pop() *muxPackager {
	bucket := s.activeHead
	if bucket == nil {
		return nil
	}
	s.activeHead = bucket.next
	if s.activeHead == nil {
		s.activeTail = nil
	}
	bucket.next = nil

	packager := bucket.head
	bucket.head = packager.queueNext
	packager.queueNext = nil
	if bucket.head == nil {
		delete(s.buckets, bucket.id)
		*bucket = streamBucket{}
		streamBucketPool.Put(bucket)
	} else {
		s.appendActive(bucket)
	}
	return packager
}

func (s *streamBucketSet) appendActive(bucket *streamBucket) {
	if s.activeTail == nil {
		s.activeHead = bucket
	} else {
		s.activeTail.next = bucket
	}
	s.activeTail = bucket
}
