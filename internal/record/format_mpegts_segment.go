package record

import (
	"os"
	"path/filepath"
	"time"

	"github.com/bluenviron/mediamtx/internal/logger"
)

type formatMPEGTSSegment struct {
	f        *formatMPEGTS
	startDTS time.Duration
	startNTP time.Time

	path      string
	fi        *os.File
	lastFlush time.Duration
	lastDTS   time.Duration
}

func (s *formatMPEGTSSegment) initialize() {
	s.lastFlush = s.startDTS
	s.lastDTS = s.startDTS
	s.f.dw.setTarget(s)
}

func (s *formatMPEGTSSegment) close() error {
	err := s.f.bw.Flush()

	if s.fi != nil {
		s.f.a.agent.Log(logger.Debug, "closing segment %s", s.path)

		stat, err1 := s.fi.Stat()
		if err1 != nil {
			if err == nil {
				err = err1
			}
		}

		err2 := s.fi.Close()
		if err == nil {
			err = err2
		}

		if err2 == nil {
			duration := s.lastDTS - s.startDTS
			s.f.a.agent.OnSegmentComplete(s.path, duration, stat.Size())
		}
	}

	return err
}

func (s *formatMPEGTSSegment) Write(p []byte) (int, error) {
	if s.fi == nil {
		s.path = Path{Start: s.startNTP}.Encode(s.f.a.pathFormat)
		s.f.a.agent.Log(logger.Debug, "creating segment %s", s.path)

		err := os.MkdirAll(filepath.Dir(s.path), 0o755)
		if err != nil {
			return 0, err
		}

		fi, err := os.Create(s.path)
		if err != nil {
			return 0, err
		}

		s.f.a.agent.OnSegmentCreate(s.path)

		s.fi = fi
	}

	return s.fi.Write(p)
}
