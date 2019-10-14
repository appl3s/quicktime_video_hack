package usb

import (
	"encoding/binary"
	"github.com/danielpaulus/quicktime_video_hack/usb/coremedia"
	"github.com/danielpaulus/quicktime_video_hack/usb/messages"
	"github.com/danielpaulus/quicktime_video_hack/usb/packet"
	log "github.com/sirupsen/logrus"
	"os"
)

type messageProcessor struct {
	connectionState      int
	writeToUsb           func([]byte)
	stopSignal           chan interface{}
	clock                coremedia.CMClock
	totalBytesReceived   int
	needClockRef         packet.CFTypeID
	needMessage          []byte
	audioSamplesReceived int
}

func newMessageProcessor(writeToUsb func([]byte), stopSignal chan interface{}) messageProcessor {
	var mp = messageProcessor{writeToUsb: writeToUsb, stopSignal: stopSignal}
	return mp
}

func (mp *messageProcessor) receiveData(data []byte) {
	switch binary.LittleEndian.Uint32(data) {
	case packet.PingPacketMagic:
		log.Debug("initial ping received, sending ping back")
		mp.writeToUsb(packet.NewPingPacketAsBytes())
		return
	case packet.SyncPacketMagic:
		mp.handleSyncPacket(data)
		return
	case packet.AsynPacketMagic:
		mp.handleAsyncPacket(data)
		return
	default:
		log.Warnf("received unknown packet type: %x", data[:4])
	}

	var stop interface{}
	mp.stopSignal <- stop
}

func (mp *messageProcessor) handleSyncPacket(data []byte) {
	switch binary.LittleEndian.Uint32(data[12:]) {
	case packet.OG:
		ogPacket, err := packet.NewSyncOgPacketFromBytes(data)
		if err != nil {
			log.Error("Error parsing OG packet", err)
		}
		log.Debugf("Rcv:%s", ogPacket.String())

		replyBytes := ogPacket.NewReply()
		log.Debugf("Send OG-REPLY {correlation:%x}", ogPacket.CorrelationID)
		mp.writeToUsb(replyBytes)
	case packet.CWPA:
		cwpaPacket, err := packet.NewSyncCwpaPacketFromBytes(data)
		if err != nil {
			log.Error("failed parsing cwpa packet", err)
			return
		}
		log.Debugf("Rcv:%s", cwpaPacket.String())
		clockRef := cwpaPacket.DeviceClockRef + 1000

		deviceInfo := packet.NewAsynHpd1Packet(messages.CreateHpd1DeviceInfoDict())
		log.Debug("Sending ASYN HPD1")
		mp.writeToUsb(deviceInfo)
		log.Debugf("Send CWPA-RPLY {correlation:%x, clockRef:%x}", cwpaPacket.CorrelationID, clockRef)
		mp.writeToUsb(cwpaPacket.NewReply(clockRef))
		log.Debug("Sending ASYN HPD1")
		mp.writeToUsb(deviceInfo)
		deviceInfo1 := packet.NewAsynHpa1Packet(messages.CreateHpa1DeviceInfoDict(), cwpaPacket.DeviceClockRef)
		log.Debug("Sending ASYN HPA1")
		mp.writeToUsb(deviceInfo1)
	case packet.CVRP:
		cvrpPacket, err := packet.NewSyncCvrpPacketFromBytes(data)
		if err != nil {
			log.Error("Error parsing CVRP packet", err)
			return
		}

		log.Debugf("Rcv:%s", cvrpPacket.String())

		mp.needClockRef = cvrpPacket.DeviceClockRef
		mp.needMessage = packet.AsynNeedPacketBytes(mp.needClockRef)
		log.Debugf("Send NEED %x", mp.needClockRef)
		mp.writeToUsb(mp.needMessage)

		clockRef2 := cvrpPacket.DeviceClockRef + 0x1000AF
		log.Debugf("Send CVRP-RPLY {correlation:%x, clockRef:%x}", cvrpPacket.CorrelationID, clockRef2)
		mp.writeToUsb(cvrpPacket.NewReply(clockRef2))
	case packet.CLOK:
		clokPacket, err := packet.NewSyncClokPacketFromBytes(data)
		if err != nil {
			log.Error("Failed parsing Clok Packet", err)
		}
		log.Debugf("Rcv:%s", clokPacket.String())
		clockRef := clokPacket.ClockRef + 0x10000
		mp.clock = coremedia.NewCMClockWithHostTime(clockRef)
		log.Debugf("Send CLOK-RPLY {correlation:%x, clockRef:%x}", clokPacket.CorrelationID, clockRef)
		mp.writeToUsb(clokPacket.NewReply(clockRef))
	case packet.TIME:
		timePacket, err := packet.NewSyncTimePacketFromBytes(data)
		if err != nil {
			log.Error("Error parsing TIME SYNC packet", err)
		}
		log.Debugf("Rcv:%s", timePacket.String())
		timeToSend := mp.clock.GetTime()
		replyBytes, err := timePacket.NewReply(timeToSend)
		if err != nil {
			log.Error("Could not create SYNC TIME REPLY")
		}
		log.Debugf("Send TIME-REPLY {correlation:%x, time:%s}", timePacket.CorrelationID, timeToSend)
		mp.writeToUsb(replyBytes)
	case packet.AFMT:
		afmtPacket, err := packet.NewSyncAfmtPacketFromBytes(data)
		if err != nil {
			log.Error("Error parsing TIME AFMT packet", err)
		}
		log.Debugf("Rcv:%s", afmtPacket.String())

		replyBytes := afmtPacket.NewReply()
		log.Debugf("Send AFMT-REPLY {correlation:%x}", afmtPacket.CorrelationID)
		mp.writeToUsb(replyBytes)
	default:
		log.Warnf("received unknown sync packet type: %x", data)
	}
}

func (mp *messageProcessor) handleAsyncPacket(data []byte) {
	switch binary.LittleEndian.Uint32(data[12:]) {
	case packet.EAT:
		mp.audioSamplesReceived++
		if mp.audioSamplesReceived%100 == 0 {
			log.Debugf("RCV Audio Samples:%d", mp.audioSamplesReceived)
		}
	case packet.FEED:
		feedPacket, err := packet.NewAsynFeedPacketFromBytes(data)
		if err != nil {
			//log.Errorf("Error parsing FEED packet: %x %s", data, err)
			log.Warn("unknown feed")
			return
		}
		log.Debugf("Rcv:%s", feedPacket.String())
		mp.writeToUsb(mp.needMessage)
	case packet.SPRP:
		sprpPacket, err := packet.NewAsynSprpPacketFromBytes(data)
		if err != nil {
			log.Error("Error parsing SPRP packet", err)
			return
		}
		log.Debugf("Rcv:%s", sprpPacket.String())
	case packet.TJMP:
		tjmpPacket, err := packet.NewAsynTjmpPacketFromBytes(data)
		if err != nil {
			log.Error("Error parsing tjmp packet", err)
			return
		}
		log.Debugf("Rcv:%s", tjmpPacket.String())
	case packet.SRAT:
		sratPacket, err := packet.NewAsynSratPacketFromBytes(data)
		if err != nil {
			log.Error("Error parsing srat packet", err)
			return
		}
		log.Debugf("Rcv:%s", sratPacket.String())
	case packet.TBAS:
		tbasPacket, err := packet.NewAsynTbasPacketFromBytes(data)
		if err != nil {
			log.Error("Error parsing tbas packet", err)
			return
		}
		log.Debugf("Rcv:%s", tbasPacket.String())
	default:
		log.Warnf("received unknown async packet type: %x", data)
		os.Exit(1)
	}
}
