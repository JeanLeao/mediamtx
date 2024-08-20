package playback

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/ioutil"
	"net"
	"net/http"
	"os"
	"strconv"
	"time"

	"github.com/bluenviron/mediacommon/pkg/formats/fmp4"
	"github.com/bluenviron/mediamtx/internal/conf"
	"github.com/bluenviron/mediamtx/internal/logger"
	"github.com/gin-gonic/gin"
)

type writerWrapper struct {
	ctx     *gin.Context
	written bool
}

type Recording struct {
	Path     string  `json:"path"`
	Duration float64 `json:"duration"`
	Start    string  `json:"start"`
}

func (w *writerWrapper) Write(p []byte) (int, error) {
	if !w.written {
		w.written = true
		w.ctx.Header("Accept-Ranges", "none")
		w.ctx.Header("Content-Type", "video/mp4")
	}
	return w.ctx.Writer.Write(p)
}

func parseDuration(raw string) (time.Duration, error) {
	// seconds
	if secs, err := strconv.ParseFloat(raw, 64); err == nil {
		return time.Duration(secs * float64(time.Second)), nil
	}

	// deprecated, golang format
	return time.ParseDuration(raw)
}

func seekAndMux(
	recordFormat conf.RecordFormat,
	segments []*Segment,
	start time.Time,
	duration time.Duration,
	m muxer,
) error {
	if recordFormat == conf.RecordFormatFMP4 {
		var firstInit *fmp4.Init
		var segmentEnd time.Time

		f, err := os.Open(segments[0].Fpath)
		if err != nil {
			return err
		}
		defer f.Close()

		fmt.Printf("Segmento ENCONTRADO: %+v\n", *f)
		firstInit, err = segmentFMP4ReadInit(f)
		if err != nil {
			return err
		}

		fmt.Printf("firstInit: %+v\n", *firstInit)

		m.writeInit(firstInit)

		segmentStartOffset := start.Sub(segments[0].Start)

		fmt.Printf("segmentStartOffset: %+v\n", segmentStartOffset)

		segmentMaxElapsed, err := segmentFMP4SeekAndMuxParts(f, segmentStartOffset, duration, firstInit, m)
		if err != nil {
			return err
		}

		segmentEnd = start.Add(segmentMaxElapsed)

		for _, seg := range segments[1:] {
			f, err = os.Open(seg.Fpath)
			if err != nil {
				return err
			}
			defer f.Close()

			var init *fmp4.Init
			init, err = segmentFMP4ReadInit(f)
			if err != nil {
				return err
			}

			if !segmentFMP4CanBeConcatenated(firstInit, segmentEnd, init, seg.Start) {
				break
			}

			segmentStartOffset := seg.Start.Sub(start)

			var segmentMaxElapsed time.Duration
			segmentMaxElapsed, err = segmentFMP4MuxParts(f, segmentStartOffset, duration, firstInit, m)
			if err != nil {
				return err
			}

			segmentEnd = start.Add(segmentMaxElapsed)
		}

		err = m.flush()
		if err != nil {
			return err
		}

		return nil
	}

	return fmt.Errorf("MPEG-TS format is not supported yet")
}

func (p *Server) onGet(ctx *gin.Context) {
	pathName := ctx.Query("path")

	if !p.doAuth(ctx, pathName) {
		return
	}

	start, err := time.Parse(time.RFC3339, ctx.Query("start"))
	if err != nil {
		p.writeError(ctx, http.StatusBadRequest, fmt.Errorf("invalid start: %w", err))
		return
	}

	duration, err := parseDuration(ctx.Query("duration"))
	if err != nil {
		p.writeError(ctx, http.StatusBadRequest, fmt.Errorf("invalid duration: %w", err))
		return
	}

	ww := &writerWrapper{ctx: ctx}
	var m muxer

	format := ctx.Query("format")
	switch format {
	case "", "fmp4":
		m = &muxerFMP4{w: ww}

	case "mp4":
		m = &muxerMP4{w: ww}

	default:
		p.writeError(ctx, http.StatusBadRequest, fmt.Errorf("invalid format: %s", format))
		return
	}

	pathConf, err := p.safeFindPathConf(pathName)
	if err != nil {
		p.writeError(ctx, http.StatusBadRequest, err)
		return
	}

	// segments, err := findSegmentsInTimespan(pathConf, pathName, start, duration)
	// if err != nil {
	// 	if errors.Is(err, errNoSegmentsFound) {
	// 		p.writeError(ctx, http.StatusNotFound, err)
	// 	} else {
	// 		p.writeError(ctx, http.StatusBadRequest, err)
	// 	}
	// 	return
	// }

	url := fmt.Sprintf("%s/api/v1/recording/list-without-concatenation?device=%s&start=%s&duration=%s", p.MicroServiceUrl, pathName, ctx.Query("start"), ctx.Query("duration"))
	fmt.Println("URL:", url)
	resp, err := http.Get(url)
	if err != nil {
		fmt.Println("Erro ao fazer a requisição:", err)
		return
	}
	defer resp.Body.Close()

	// Lendo o corpo da resposta
	body, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		fmt.Println("Erro ao ler o corpo da resposta:", err)
		return
	}

	// Deserializando o corpo da resposta para um slice de Recording
	var recordings []Recording
	err = json.Unmarshal(body, &recordings)
	if err != nil {
		fmt.Println("Erro ao deserializar o JSON:", err)
		return
	}

	// Check if recordings is empty
	if len(recordings) == 0 {
		fmt.Println("Nenhuma gravação encontrada.")
		return
	}

	// Imprimindo o JSON deserializado
	for _, recording := range recordings {
		fmt.Printf("Path: %s\nStart: %s\n", recording.Path, recording.Start)
	}

	segments := make([]*Segment, len(recordings))
	for i, recording := range recordings {

		parsedTime, err := time.Parse(time.RFC3339, recording.Start)
		if err != nil {
			fmt.Println("Erro ao converter a string para time.Time:", err)
			return
		}

		year, month, day := parsedTime.Date()
		hour, min, sec := parsedTime.Clock()
		nsec := parsedTime.Nanosecond()

		segments[i] = &Segment{
			Fpath: recording.Path,
			Start: time.Date(year, month, day, hour, min, sec, nsec, time.UTC),
		}
	}

	fmt.Println("Segmentos encontrados:")
	for i, segment := range segments {
		fmt.Printf("Segmento %d: %+v\n", i, *segment)
	}

	// local := "/home/jean/Documentos/mediamtx/mediamtx"

	// Fpath string
	// Start time.Time
	// "2024-08-08 17:13:29.437452 -0300 -03"
	// 2024-08-08_17-42-02-527827.mp4
	// foo := []*Segment{
	// 	{Fpath: "/home/jean/Documentos/mediamtx/mediamtx/recordings/66abfd2b96c48cd25ab58604/2024-08-08_17-13-29-437452.mp4", Start: time.Date(2024, 8, 8, 17, 42, 02, 527827, time.FixedZone("UTC-3", -3*60*60))},
	// }

	err = seekAndMux(pathConf.RecordFormat, segments, start, duration, m)
	if err != nil {
		// user aborted the download
		var neterr *net.OpError
		if errors.As(err, &neterr) {
			return
		}

		// nothing has been written yet; send back JSON
		if !ww.written {
			if errors.Is(err, errNoSegmentsFound) {
				p.writeError(ctx, http.StatusNotFound, err)
			} else {
				p.writeError(ctx, http.StatusBadRequest, err)
			}
			return
		}

		// something has already been written: abort and write logs only
		p.Log(logger.Error, err.Error())
		return
	}
}
