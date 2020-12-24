package ffmpeg

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strconv"
	"time"
)

// ScreenOrientation defines if a video is Portrait or Landscape
type ScreenOrientation byte

const (
	Portrait  ScreenOrientation = 0
	Landscape ScreenOrientation = 1
)

type VideoResolution int

// ConversionPreset .
type ConversionPreset string

const (
	Ultrafast ConversionPreset = "ultrafast"
	Superfast ConversionPreset = "superfast"
	Veryfast  ConversionPreset = "veryfast"
	Faster    ConversionPreset = "faster"
	Fast      ConversionPreset = "fast"
	Medium    ConversionPreset = "medium"
	Slow      ConversionPreset = "slow"
	Slower    ConversionPreset = "slower"
	Veryslow  ConversionPreset = "veryslow"
	Placebo   ConversionPreset = "placebo"
)

// EditableVideo and Editable Video representation which  contains information about a video file and all the operations that
// need to be applied to it. Call Load to initialize a Video from file. Call the
// transformation functions to generate the desired output. Then call Render to
// generate the final output video file.
type EditableVideo Video

// Video contains information about a video file and all the operations that
// need to be applied to it. Call Load to initialize a Video from file. Call the
// transformation functions to generate the desired output. Then call Render to
// generate the final output video file.
type Video struct {
	filepath       string
	width          int
	height         int
	fps            int
	bitrate        int
	start          time.Duration
	end            time.Duration
	duration       time.Duration
	filters        []string
	additionalArgs []string
}

// GetVideoOrientation returns the video Screen Orientation
func (v *Video) GetVideoOrientation() ScreenOrientation {

	if v.width > v.height {
		return Landscape
	}
	return Portrait
}

//GetEditableVideoResolution returns the lowest value between  width and height
func (v *EditableVideo) GetEditableVideoResolution() VideoResolution {
	if v.width > v.height {
		return VideoResolution(v.height)
	}
	return VideoResolution(v.width)
}

//GetVideoResolution returns the lowest value between  width and height
func (v *Video) GetVideoResolution() VideoResolution {
	if v.width > v.height {
		return VideoResolution(v.height)
	}
	return VideoResolution(v.width)
}

//GetAspectRatio returns the Aspect Ratio
func (v *EditableVideo) GetAspectRatio() float32 {
	if v.width > v.height {
		return float32(v.width) / float32(v.height)
	}
	return float32(v.height) / float32(v.width)
}

// LoadVideo gives you a Video that can be operated on. Load does not open the file
// or load it into memory. Apply operations to the Video and call Render to
// generate the output video file.
func LoadVideo(path string) (*Video, error) {
	if _, err := exec.LookPath("ffprobe"); err != nil {
		return nil, errors.New("cinema.Load: ffprobe was not found in your PATH " +
			"environment variable, make sure to install ffmpeg " +
			"(https://ffmpeg.org/) and add ffmpeg, ffplay and ffprobe to your " +
			"PATH")
	}

	if _, err := os.Stat(path); err != nil {
		return nil, errors.New("cinema.Load: unable to load file: " + err.Error())
	}

	cmd := exec.Command(
		"ffprobe",
		"-v", "quiet",
		"-print_format", "json",
		"-show_format",
		"-show_streams",
		path,
	)
	out, err := cmd.Output()

	if err != nil {
		return nil, errors.New("cinema.Load: ffprobe failed: " + err.Error())
	}

	type description struct {
		Streams []struct {
			Width  int `json:"width"`
			Height int `json:"height"`
			Tags   struct {
				// Rotation is optional -> use a pointer.
				Rotation *json.Number `json:"rotate"`
			} `json:"tags"`
		} `json:"streams"`
		Format struct {
			DurationSec json.Number `json:"duration"`
			Bitrate     json.Number `json:"bit_rate"`
		} `json:"format"`
	}
	var desc description
	if err := json.Unmarshal(out, &desc); err != nil {
		return nil, errors.New("cinema.Load: unable to parse JSON output " +
			"from ffprobe: " + err.Error())
	}
	if len(desc.Streams) == 0 {
		return nil, errors.New("cinema.Load: ffprobe does not contain stream " +
			"data, make sure the file " + path + " contains a valid video.")
	}

	secs, err := desc.Format.DurationSec.Float64()
	if err != nil {
		return nil, errors.New("cinema.Load: ffprobe returned invalid duration: " +
			err.Error())
	}
	bitrate, err := desc.Format.Bitrate.Int64()
	if err != nil {
		return nil, errors.New("cinema.Load: ffprobe returned invalid duration: " +
			err.Error())
	}

	// Round seconds (floating point value) up to time.Duration. seconds will
	// be >= 0 so adding 0.5 rounds to the right integer Duration value.
	duration := time.Duration(secs*float64(time.Second) + 0.5)

	dsIndex := 0
	for index, v := range desc.Streams {
		if v.Width != 0 && v.Height != 0 {
			dsIndex = index
			break
		}
	}

	width := desc.Streams[dsIndex].Width
	height := desc.Streams[dsIndex].Height
	if desc.Streams[dsIndex].Tags.Rotation != nil {
		// If the video is rotated by -270, -90, 90 or 270 degrees, we need to
		// flip the width and height because they will be reported in unrotated
		// coordinates while cropping etc. works on the rotated dimensions.
		rotation, err := desc.Streams[dsIndex].Tags.Rotation.Int64()
		if err != nil {
			return nil, errors.New("cinema.Load: ffprobe returned invalid " +
				"rotation: " + err.Error())
		}
		flipCount := rotation / 90
		if flipCount%2 != 0 {
			width, height = height, width
		}
	}

	return &Video{
		filepath: path,
		width:    width,
		height:   height,
		fps:      30,
		bitrate:  int(bitrate),
		start:    0,
		end:      duration,
		duration: duration,
	}, nil
}

//GetEditableVideo returns an EditableVideo instance than can be used to safely modify a Video
func (v *Video) GetEditableVideo() *EditableVideo {
	var eVideo EditableVideo
	eVideo.filepath = v.filepath
	eVideo.width = v.width
	eVideo.height = v.height
	eVideo.fps = v.fps
	eVideo.bitrate = v.bitrate
	eVideo.start = v.start
	eVideo.end = v.end
	eVideo.duration = v.duration

	eVideo.filters = make([]string, len(v.filters))
	copy(eVideo.filters, v.filters)

	eVideo.additionalArgs = make([]string, len(v.additionalArgs))
	copy(eVideo.additionalArgs, v.additionalArgs)

	return &eVideo
}

//AddWaterMark Adds a Water mark to a video
func (v *EditableVideo) AddWaterMark(videoPath, iconPath, outputPath string, widthSize, heightSize int) error {

	cmdline := []string{
		"ffmpeg",
		"-y",
		"-i", videoPath,
		"-i", iconPath,
		"-vcodec", "libx264",
	}
	cmdline = append(cmdline, v.additionalArgs...)
	cmdline = append(cmdline, "-filter_complex")
	cmdline = append(cmdline, fmt.Sprintf("[1]scale=%d:%d[wm];[0][wm]overlay=10:10", widthSize, heightSize))
	cmdline = append(cmdline, outputPath)

	fmt.Println(cmdline)
	cmd := exec.Command(cmdline[0], cmdline[1:]...)

	var stderr bytes.Buffer

	cmd.Stderr = &stderr
	cmd.Stdout = nil

	err := cmd.Run()
	if err != nil {
		return errors.New("Video.Render: ffmpeg failed: " + stderr.String())
	}
	return nil
}

// Render applies all operations to the Video and creates an output video file
// of the given name. This method won't return anything on stdout / stderr.
// If you need to read ffmpeg's outputs, use RenderWithStreams
func (v *EditableVideo) Render(output string) error {
	return v.RenderWithStreams(output, nil, nil)
}

// RenderInBackground applies all operations to the Video and creates an output video file
// of the given name. This method won't return anything on stdout / stderr.
// If you need to read ffmpeg's outputs, use RenderWithStreams
func (v *EditableVideo) RenderInBackground(output string) (*exec.Cmd, error) {
	return v.RenderWithStreamsInBackground(output, nil)
}

// RenderWithStreamsInBackground applies all operations to the Video and creates an output video file
// of the given name. By specifying an output stream and an error stream, you can read
// ffmpeg's stdout and stderr.
func (v *EditableVideo) RenderWithStreamsInBackground(output string, os io.Writer) (*exec.Cmd, error) {
	line := v.CommandLine(output)
	fmt.Println(line)

	cmd := exec.Command(line[0], line[1:]...)

	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	cmd.Stdout = os

	err := cmd.Start()
	if err != nil {
		return nil, errors.New("cinema.Video.Render: ffmpeg failed: " + stderr.String())
	}
	return cmd, nil
}

// RenderWithStreams applies all operations to the Video and creates an output video file
// of the given name. By specifying an output stream and an error stream, you can read
// ffmpeg's stdout and stderr.
func (v *EditableVideo) RenderWithStreams(output string, os io.Writer, es io.Writer) error {
	line := v.CommandLine(output)
	fmt.Println(line)

	cmd := exec.Command(line[0], line[1:]...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	cmd.Stdout = os

	err := cmd.Run()
	if err != nil {
		return errors.New("Video.Render: ffmpeg failed: " + stderr.String())
	}
	return nil
}

// CommandLine returns the command line that will be used to convert the Video
// if you were to call Render.
func (v *EditableVideo) CommandLine(output string) []string {

	additionalArgs := v.additionalArgs

	cmdline := []string{
		"ffmpeg",
		"-y",
		"-i", v.filepath,
		"-vcodec", "libx264",
		//	"-ss", strconv.FormatFloat(v.start.Seconds(), 'f', -1, 64),
		//	"-t", strconv.FormatFloat((v.end - v.start).Seconds(), 'f', -1, 64),
		//	"-vb", strconv.Itoa(v.bitrate),
	}
	cmdline = append(cmdline, additionalArgs...)
	cmdline = append(cmdline, output)
	return cmdline
}

// Mute mutes the video
func (v *Video) Mute() {
	v.additionalArgs = append(v.additionalArgs, "-an")
}

// Trim sets the start and end time of the output video. It is always relative
// to the original input video. start must be less than or equal to end or
// nothing will change.
func (v *Video) Trim(start, end time.Duration) {
	if start <= end {
		v.SetStart(start)
		v.SetEnd(end)
	}
}

// Start returns the start of the video .
func (v *Video) Start() time.Duration {
	return v.start
}

// SetStart sets the start time of the output video. It is always relative to
// the original input video.
func (v *Video) SetStart(start time.Duration) {
	v.start = v.clampToDuration(start)
	if v.start > v.end {
		// keep c.start <= v.end
		v.end = v.start
	}
}

func (v *Video) clampToDuration(t time.Duration) time.Duration {
	if t < 0 {
		t = 0
	}
	if t > v.duration {
		t = v.duration
	}
	return t
}

// End returns the end of the video.
func (v *Video) End() time.Duration {
	return v.end
}

// SetEnd sets the end time of the output video. It is always relative to the
// original input video.
func (v *Video) SetEnd(end time.Duration) {
	v.end = v.clampToDuration(end)
	if v.end < v.start {
		// keep c.start <= v.end
		v.start = v.end
	}
}

// SetFPS sets the framerate (frames per second) of the output video.
func (v *Video) SetFPS(fps int) {
	v.fps = fps
}

// SetBitrate sets the bitrate of the output video.
func (v *Video) SetBitrate(bitrate int) {
	v.bitrate = bitrate
}

// SetSize sets the width and height of the output video.
func (v *EditableVideo) SetSize(width int, height int) {
	v.width = width
	v.height = height
	v.additionalArgs = append(v.additionalArgs, "-s")
	v.additionalArgs = append(v.additionalArgs, fmt.Sprintf("%dx%d", width, height))
}

// SetPreset  defines the Quality Compression and Speed
func (v *EditableVideo) SetPreset(preset ConversionPreset) {
	v.additionalArgs = append(v.additionalArgs, "-preset")
	v.additionalArgs = append(v.additionalArgs, string(preset))
}

// SetConstantRateFactor The range of the CRF scale is 0–51, where 0 is lossless, 23 is the default, and 51 is worst quality possible. A lower value generally leads to higher quality, and a subjectively sane range is 17–28. Consider 17 or 18 to be visually lossless or nearly so; it should look the same or nearly the same as the input but it isn't technically lossless. The range is exponential, so increasing the CRF value +6 results in roughly half the bitrate / file size, while -6 leads to roughly twice the bitrate. Choose the highest CRF value that still provides an acceptable quality. If the output looks good, then try a higher value. If it looks bad, choose a lower value.
func (v *EditableVideo) SetConstantRateFactor(value int) {
	v.additionalArgs = append(v.additionalArgs, "-crf")
	v.additionalArgs = append(v.additionalArgs, strconv.Itoa(value))
}

func isEvenNumber(n int) bool {
	return n%2 == 0
}

func toEvenNumber(n int) int {
	if isEvenNumber(n) {
		return n
	}
	return n + 1
}

//GetResolutions returns the video (Width,Height) tuple for a specific VideoResolution
func (v *EditableVideo) GetResolutions(res VideoResolution) (int, int) {
	aspectRatio := v.GetAspectRatio()
	maxSize := toEvenNumber(int(float32(res) * aspectRatio))

	if v.width > v.height {
		return maxSize, int(res)
	}

	return int(res), maxSize
}

// GetFilePath returns the path of the input video.
func (v *EditableVideo) GetFilePath() string {
	return v.filepath
}

// SetResolution sets the  Resolution respecting the Aspect Ratio of the Original Video.
func (v *EditableVideo) SetResolution(res VideoResolution) {
	aspectRatio := v.GetAspectRatio()
	maxSize := toEvenNumber(int(float32(res) * aspectRatio))

	if v.width > v.height {
		v.SetSize(maxSize, int(res))
	} else {
		v.SetSize(int(res), maxSize)
	}
}

//SetFilePath set the filepath for a video
func (v *Video) SetFilePath(p string) {
	v.filepath = p
}

// Crop makes the output video a sub-rectangle of the input video. (0,0) is the
// top-left of the video, x goes right, y goes down.
func (v *Video) Crop(x, y, width, height int) {
	v.width = width
	v.height = height
	v.filters = append(
		v.filters,
		fmt.Sprintf("crop=%d:%d:%d:%d", width, height, x, y),
	)
}

// Filepath returns the path of the input video.
func (v *Video) Filepath() string {
	return v.filepath
}