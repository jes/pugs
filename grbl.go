package main

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"time"
)

type Grbl struct {
	SerialPort      io.ReadWriteCloser
	PortName        string
	Closed          bool
	Status          string
	Wco             V4d
	Mpos            V4d
	Wpos            V4d
	Dtg             V4d // TODO: how can we calculate this?
	Vel             V4d
	PlannerSize     int
	PlannerFree     int
	SerialSize      int
	SerialFree      int
	SpindleCw       bool
	SpindleCcw      bool
	FloodCoolant    bool
	MistCoolant     bool
	FeedOverride    float64
	RapidOverride   float64
	SpindleOverride float64
	FeedRate        float64
	SpindleSpeed    float64
	Pn              string
	Probe           bool
	StatusUpdate    chan struct{}
	UpdateTime      time.Time
	ResponseQueue   []chan string
	ResponseLock    sync.Mutex
	GCodes          string
}

func NewGrbl(port io.ReadWriteCloser, portName string) *Grbl {
	g := &Grbl{
		SerialPort:   port,
		PortName:     portName,
		Status:       "Connecting",
		StatusUpdate: make(chan struct{}),
	}
	if port == nil {
		g.Status = "Disconnected"
		g.Closed = true
	}
	return g
}

// add the given line to the command queue, returning a channel
// which will receive the response if the command was added to
// the queue, or nil if the queue is full
//
// only use this function for commands that expect a response,
// use CommandRealtime() for commands that give no response
func (g *Grbl) Command(line string) chan string {
	if g.Closed {
		return nil
	}

	// not enough space in Grbl's input buffer? reject the command
	if g.SerialFree <= len(line)+1 {
		return nil
	}

	responseChan := make(chan string)
	g.ResponseLock.Lock()
	g.ResponseQueue = append(g.ResponseQueue, responseChan)
	g.ResponseLock.Unlock()

	_, err := g.Write([]byte(line + "\n"))
	if err != nil {
		fmt.Fprintf(os.Stderr, "error from %s: %v\n", g.PortName, err)
		g.Close()
		return nil
	}

	return responseChan
}

// add the given line to the command queue, return true if
// successful or false if not
//
// spawn a goroutine to consume and ignore the response
func (g *Grbl) CommandIgnore(line string) bool {
	if g.Closed {
		return false
	}
	c := g.Command(line)
	if c == nil {
		return false
	}
	go func() { <-c }() // ignore response
	return true
}

// send the given realtime command, return true if successful
// or false if not
func (g *Grbl) CommandRealtime(cmd byte) bool {
	if g.Closed {
		return false
	}
	_, err := g.Write([]byte{cmd})
	if err != nil {
		fmt.Fprintf(os.Stderr, "error from %s: %v\n", g.PortName, err)
		g.Close()
		return false
	}
	return true
}

// implements io.Writer
func (g *Grbl) Write(p []byte) (n int, err error) {
	// TODO: is there a race condition where concurrent writes can end up interleaved?
	// TODO: is there a race condition where we decrease SerialFree, then read a status report that still has the old SerialFree in it,
	// and then send some more bytes but the buffer is already full?
	g.SerialFree -= len(p)
	os.Stdout.Write(p)
	return g.SerialPort.Write(p)
}

// implements io.Closer
func (g *Grbl) Close() error {
	if g.Closed {
		return nil
	}
	g.Closed = true
	g.Status = "Disconnected"
	var err error
	if g.SerialPort != nil {
		err = g.SerialPort.Close()
	}
	close(g.StatusUpdate)
	return err
}

func (g *Grbl) Monitor() {
	if g.SerialPort == nil {
		g.Close()
		return
	}

	// ask for a status update every 100ms, until Closed
	ticker := time.NewTicker(100 * time.Millisecond)
	g.RequestStatusUpdate()
	go func() {
		for {
			<-ticker.C
			if !g.RequestStatusUpdate() {
				break
			}
		}
		ticker.Stop()
	}()

	// ask for active g-codes every second, until closed
	ticker2 := time.NewTicker(time.Second)
	g.RequestGCodes()
	go func() {
		for {
			<-ticker2.C
			if !g.RequestGCodes() {
				break
			}
		}
		ticker2.Stop()
	}()

	// read from the serial port
	scanner := bufio.NewScanner(g.SerialPort)
	for scanner.Scan() {
		line := scanner.Text()
		fmt.Println(scanner.Text())
		if strings.HasPrefix(line, "<") && strings.HasSuffix(line, ">") {
			// status update
			g.ParseStatus(line)
		} else if strings.HasPrefix(line, "[GC:") {
			// g-codes update
			g.ParseGCodes(line)
		} else if strings.HasPrefix(line, "ok") || strings.HasPrefix(line, "error") {
			g.SendResponse(line)
		}
	}
	g.Close()
}

// request a status update, return true if ok or false if not
func (g *Grbl) RequestStatusUpdate() bool {
	return g.CommandRealtime('?')
}

// request active gcodes, return true if ok or false if not
func (g *Grbl) RequestGCodes() bool {
	if g.Closed {
		return false
	}
	// TODO: also request gcodes whenever we think they might have changed?
	ok := g.CommandIgnore("$G")
	if !ok && g.Closed {
		return false
	}
	return true
}

// "status" should be a status report line from Grbl
// send a struct{} to the StatusUpdate channel whenever there isa new status report
func (g *Grbl) ParseStatus(status string) {
	prevWpos := g.Wpos
	prevUpdateTime := g.UpdateTime

	status = strings.Trim(status, "<>")
	parts := strings.Split(status, "|")
	g.Status = parts[0]

	if g.GCodes == "" {
		// at startup, get the active g-codes without having to wait for the timer to fire
		g.RequestGCodes()
	}

	// grbl in theory should give us either a wpos or an mpos
	// every time, but track them separately just in case
	givenWpos := false
	givenMpos := false

	newProbeState := false
	newPn := ""

	for _, part := range parts[1:] {
		keyval := strings.SplitN(part, ":", 2)
		if len(keyval) != 2 {
			fmt.Fprintf(os.Stderr, "unrecognised status item [%s]\n", part)
			continue
		}
		key := keyval[0]
		keylc := strings.ToLower(key)
		val := keyval[1]
		valv4d, _ := ParseV4d(val)

		if keylc == "wpos" { // work position
			givenWpos = true
			g.Wpos = valv4d
		} else if keylc == "mpos" { // machine position
			givenMpos = true
			g.Mpos = valv4d
		} else if keylc == "wco" { // work coordinate offset
			g.Wco = valv4d
		} else if keylc == "ov" { // overrides
			g.FeedOverride = valv4d.X
			g.RapidOverride = valv4d.X
			g.SpindleOverride = valv4d.X
		} else if keylc == "a" { // accessories
			g.SpindleCw = strings.Contains(val, "S")
			g.SpindleCcw = strings.Contains(val, "C")
			g.FloodCoolant = strings.Contains(val, "F")
			g.MistCoolant = strings.Contains(val, "M")
		} else if keylc == "bf" { // buffers
			g.PlannerFree = int(valv4d.X)
			g.SerialFree = int(valv4d.Y)
			if g.PlannerFree > g.PlannerSize {
				g.PlannerSize = g.PlannerFree
			}
			if g.SerialFree > g.SerialSize {
				g.SerialSize = g.SerialFree
			}
		} else if keylc == "fs" { // feed/speed
			g.FeedRate = valv4d.X
			g.SpindleSpeed = valv4d.Y
		} else if keylc == "f" { // feed rate
			g.FeedRate = valv4d.X
		} else if keylc == "pn" { // pins
			newProbeState = strings.Contains(val, "P")
			newPn = val
		} else {
			fmt.Fprintf(os.Stderr, "unrecognised field: %s\n", key)
		}
	}

	g.Probe = newProbeState
	g.Pn = newPn

	if givenMpos {
		g.Wpos = g.Mpos.Add(g.Wco)
	} else if givenWpos {
		g.Mpos = g.Wpos.Sub(g.Wco)
	}

	g.UpdateTime = time.Now()

	distanceMoved := g.Wpos.Sub(prevWpos)
	g.Vel = distanceMoved.Div(g.UpdateTime.Sub(prevUpdateTime).Minutes())

	g.StatusUpdate <- struct{}{}
}

func (g *Grbl) ParseGCodes(line string) {
	g.GCodes = strings.TrimRight(strings.TrimPrefix(line, "[GC:"), "]")
}

func (g *Grbl) SendResponse(line string) {
	g.ResponseLock.Lock()
	defer g.ResponseLock.Unlock()

	l := len(g.ResponseQueue)
	if l == 0 {
		fmt.Fprintf(os.Stderr, "BUG: wanted to send a command response, but no channels are waiting; this means the sender is out of sync\n")
		return
	}

	responseChan := g.ResponseQueue[0]
	g.ResponseQueue = g.ResponseQueue[1:]
	responseChan <- line
	close(responseChan)
}

// extrapolated Wpos
func (g *Grbl) WposExt() V4d {
	dt := time.Now().Sub(g.UpdateTime)
	return g.Wpos.Add(g.Vel.Mul(dt.Minutes()))
}

// extrapolated Mpos
func (g *Grbl) MposExt() V4d {
	dt := time.Now().Sub(g.UpdateTime)
	return g.Mpos.Add(g.Vel.Mul(dt.Minutes()))
}

func (g *Grbl) SetWpos(v V4d) bool {
	return g.CommandIgnore(fmt.Sprintf("G10L20P1X%.03fY%.03f", v.X, v.Y))
}
