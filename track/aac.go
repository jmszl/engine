package track

import (
	"io"
	"net"

	"go.uber.org/zap"
	"m7s.live/engine/v4/codec"
	. "m7s.live/engine/v4/common"
	"m7s.live/engine/v4/util"
)

var _ SpesificTrack = (*AAC)(nil)

func NewAAC(puber IPuber, stuff ...any) (aac *AAC) {
	aac = &AAC{
		Mode: 2,
	}
	aac.AACDecoder.SizeLength = 13
	aac.AACDecoder.IndexLength = 3
	aac.AACDecoder.IndexDeltaLength = 3
	aac.CodecID = codec.CodecID_AAC
	aac.Channels = 2
	aac.SampleSize = 16
	aac.SetStuff("aac", byte(97), aac, stuff, puber)
	if aac.BytesPool == nil {
		aac.BytesPool = make(util.BytesPool, 17)
	}
	aac.AVCCHead = []byte{0xAF, 1}
	return
}

type AAC struct {
	Audio
	Mode      int       // 1为lbr，2为hbr
	fragments *util.BLL // 用于处理不完整的AU,缺少的字节数
}

func (aac *AAC) WriteADTS(ts uint32, b util.IBytes) {
	adts := b.Bytes()
	if aac.SequenceHead == nil {
		profile := ((adts[2] & 0xc0) >> 6) + 1
		sampleRate := (adts[2] & 0x3c) >> 2
		channel := ((adts[2] & 0x1) << 2) | ((adts[3] & 0xc0) >> 6)
		config1 := (profile << 3) | ((sampleRate & 0xe) >> 1)
		config2 := ((sampleRate & 0x1) << 7) | (channel << 3)
		aac.Media.WriteSequenceHead([]byte{0xAF, 0x00, config1, config2})
		aac.SampleRate = uint32(codec.SamplingFrequencies[sampleRate])
		aac.Channels = channel
		aac.Parse(aac.SequenceHead[2:])
		aac.iframeReceived = true
		aac.Attach()
	}
	aac.generateTimestamp(ts)
	frameLen := (int(adts[3]&3) << 11) | (int(adts[4]) << 3) | (int(adts[5]) >> 5)
	for len(adts) >= frameLen {
		aac.Value.AUList.Push(aac.BytesPool.GetShell(adts[7:frameLen]))
		adts = adts[frameLen:]
		if len(adts) < 7 {
			break
		}
		frameLen = (int(adts[3]&3) << 11) | (int(adts[4]) << 3) | (int(adts[5]) >> 5)
	}
	aac.Value.ADTS = aac.GetFromPool(b)
	aac.Flush()
}

// https://datatracker.ietf.org/doc/html/rfc3640#section-3.2.1
func (aac *AAC) WriteRTPFrame(rtpItem *LIRTP) {
	aac.Value.RTP.Push(rtpItem)
	frame := &rtpItem.Value
	au, err := aac.AACDecoder.Decode(frame.Packet)
	if err != nil {
		aac.Error("decode error", zap.Error(err))
		return
	}
	if len(au) > 0 {
		if aac.SampleRate != 90000 {
			aac.generateTimestamp(uint32(uint64(frame.Timestamp) * 90000 / uint64(aac.SampleRate)))
		}
		aac.AppendAuBytes(au...)
		aac.Flush()
	}
}

func (aac *AAC) WriteSequenceHead(sh []byte) error {
	aac.Media.WriteSequenceHead(sh)
	config1, config2 := aac.SequenceHead[2], aac.SequenceHead[3]
	aac.Channels = ((config2 >> 3) & 0x0F) //声道
	aac.SampleRate = uint32(codec.SamplingFrequencies[((config1&0x7)<<1)|(config2>>7)])
	aac.Parse(aac.SequenceHead[2:])
	go aac.Attach()
	return nil
}

func (aac *AAC) WriteAVCC(ts uint32, frame *util.BLL) error {
	if l := frame.ByteLength; l < 4 {
		aac.Error("AVCC data too short", zap.Int("len", l))
		return io.ErrShortWrite
	}
	if frame.GetByte(1) == 0 {
		aac.WriteSequenceHead(frame.ToBytes())
		frame.Recycle()
	} else {
		au := frame.ToBuffers()
		au[0] = au[0][2:]
		aac.AppendAuBytes(au...)
		aac.Audio.WriteAVCC(ts, frame)
	}
	return nil
}

func (aac *AAC) CompleteRTP(value *AVFrame) {
	l := value.AUList.ByteLength
	//AU_HEADER_LENGTH,因为单位是bit, 除以8就是auHeader的字节长度；又因为单个auheader字节长度2字节，所以再除以2就是auheader的个数。
	auHeaderLen := []byte{0x00, 0x10, (byte)((l & 0x1fe0) >> 5), (byte)((l & 0x1f) << 3)} // 3 = 16-13, 5 = 8-3
	var packets [][][]byte
	r := value.AUList.Next.Value.NewReader()
	for bufs := r.ReadN(RTPMTU); len(bufs) > 0; bufs = r.ReadN(RTPMTU) {
		packets = append(packets, append(net.Buffers{auHeaderLen}, bufs...))
	}
	aac.PacketizeRTP(packets...)
}
