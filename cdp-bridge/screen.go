package main

// `cdp-bridge screen` is the agent's view of the desktop: a small VNC (RFB 3.8) client
// for unwrap's -vnc-listen port. It covers what CDP can't reach - Chrome's own UI
// (toolbar, extension popups, permission prompts, menus) and the rest of the desktop -
// with one command per action, so an agent drives it from a shell.
//
// Only what wayvnc needs is implemented: no-auth security, 32-bit true colour, Raw
// encoding, pointer and key events. Each command opens its own connection.

import (
	"bufio"
	"encoding/binary"
	"errors"
	"flag"
	"fmt"
	"image"
	"image/png"
	"io"
	"net"
	"os"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

const screenUsage = `usage: cdp-bridge screen <command> [args]

  shot [-o FILE] [-crop X,Y,W,H]   save the screen as PNG (default /tmp/chrome-screen.png)
  size                             print the screen size
  move X Y                         move the pointer
  click X Y [-button left|middle|right] [-double]
  drag X1 Y1 X2 Y2                 press at X1,Y1, move, release at X2,Y2 (left button)
  scroll X Y up|down [N]           N wheel steps at X,Y (default 3)
  type TEXT                        type text (keyboard layout characters; not IME input)
  key COMBO...                     press keys, e.g. Return, ctrl+l, alt+F4, ctrl+shift+t

Coordinates are screen pixels, the same as in the PNG from shot.
The VNC address is $CHROME_SCREEN_VNC (default 127.0.0.1:5900).`

const inputSettle = 150 * time.Millisecond

func screenMain(args []string) int {
	if len(args) == 0 || args[0] == "-h" || args[0] == "--help" || args[0] == "help" {
		fmt.Println(screenUsage)
		return 0
	}
	addr := os.Getenv("CHROME_SCREEN_VNC")
	if addr == "" {
		addr = "127.0.0.1:5900"
	}
	if err := screenCommand(addr, args[0], args[1:]); err != nil {
		fmt.Fprintf(os.Stderr, "screen %s: %v\n", args[0], err)
		return 1
	}
	return 0
}

func screenCommand(addr, cmd string, args []string) (err error) {
	fs := flag.NewFlagSet(cmd, flag.ContinueOnError)
	out := fs.String("o", "/tmp/chrome-screen.png", "output file")
	crop := fs.String("crop", "", "X,Y,W,H")
	button := fs.String("button", "left", "left, middle or right")
	double := fs.Bool("double", false, "double-click")
	if err := fs.Parse(reorderFlags(args)); err != nil {
		return err
	}
	pos := fs.Args()
	ints := func(n int) ([]int, error) {
		if len(pos) < n {
			return nil, fmt.Errorf("needs %d numbers", n)
		}
		v := make([]int, n)
		for i := range v {
			x, err := strconv.Atoi(pos[i])
			if err != nil {
				return nil, fmt.Errorf("%q is not a number", pos[i])
			}
			v[i] = x
		}
		return v, nil
	}

	v, err := dialRFB(addr)
	if err != nil {
		return err
	}
	defer v.Close()
	if cmd != "size" && cmd != "shot" {
		if err := v.sync(); err != nil {
			return err
		}
		// wayvnc drops a new client's first input events while it sets up that client's
		// virtual keyboard and pointer. Measured through the tunnel: with no pause a
		// click did nothing and `key ctrl+a` / the first typed key were lost; 50 ms was
		// already enough.
		time.Sleep(inputSettle)
		defer func() {
			if err == nil {
				err = v.sync()
			}
		}()
	}

	switch cmd {
	case "size":
		fmt.Printf("%dx%d\n", v.w, v.h)
	case "shot":
		img, err := v.screenshot()
		if err != nil {
			return err
		}
		var sub image.Image = img
		if *crop != "" {
			r, err := parseRect(*crop)
			if err != nil {
				return err
			}
			sub = img.SubImage(r.Intersect(img.Rect))
		}
		f, err := os.Create(*out)
		if err != nil {
			return err
		}
		if err := png.Encode(f, sub); err != nil {
			f.Close()
			return err
		}
		if err := f.Close(); err != nil {
			return err
		}
		b := sub.Bounds()
		fmt.Printf("%s %dx%d at %d,%d (screen %dx%d)\n", *out, b.Dx(), b.Dy(), b.Min.X, b.Min.Y, v.w, v.h)
	case "move":
		p, err := ints(2)
		if err != nil {
			return err
		}
		return v.pointer(0, p[0], p[1])
	case "click":
		p, err := ints(2)
		if err != nil {
			return err
		}
		mask, ok := map[string]uint8{"left": 1, "middle": 2, "right": 4}[*button]
		if !ok {
			return fmt.Errorf("unknown button %q", *button)
		}
		times := 1
		if *double {
			times = 2
		}
		if err := v.pointer(0, p[0], p[1]); err != nil {
			return err
		}
		for range times {
			if err := v.pressRelease(mask, p[0], p[1]); err != nil {
				return err
			}
		}
	case "drag":
		p, err := ints(4)
		if err != nil {
			return err
		}
		if err := v.pointer(0, p[0], p[1]); err != nil {
			return err
		}
		if err := v.pointer(1, p[0], p[1]); err != nil {
			return err
		}
		// In steps, so the application sees motion rather than a jump.
		const steps = 12
		for i := 1; i <= steps; i++ {
			x := p[0] + (p[2]-p[0])*i/steps
			y := p[1] + (p[3]-p[1])*i/steps
			if err := v.pointer(1, x, y); err != nil {
				return err
			}
			time.Sleep(15 * time.Millisecond)
		}
		return v.pointer(0, p[2], p[3])
	case "scroll":
		p, err := ints(2)
		if err != nil {
			return err
		}
		if len(pos) < 3 {
			return errors.New("needs up or down")
		}
		mask, ok := map[string]uint8{"up": 8, "down": 16}[pos[2]]
		if !ok {
			return fmt.Errorf("unknown direction %q", pos[2])
		}
		n := 3
		if len(pos) > 3 {
			if n, err = strconv.Atoi(pos[3]); err != nil || n < 1 {
				return fmt.Errorf("%q is not a step count", pos[3])
			}
		}
		if err := v.pointer(0, p[0], p[1]); err != nil {
			return err
		}
		for range n {
			if err := v.pressRelease(mask, p[0], p[1]); err != nil {
				return err
			}
		}
	case "type":
		if len(pos) == 0 {
			return errors.New("needs text")
		}
		text := strings.Join(pos, " ")
		// wayvnc types a keysym only if the keyboard layout (US) has it, and drops the rest
		// without an error (measured: "Hello 한글" typed "Hello"). Refuse up front
		// rather than report success for text that never arrived.
		for _, r := range text {
			if r > 0x7e || (r < 0x20 && r != '\n' && r != '\t') {
				return fmt.Errorf("%q is not on the keyboard layout; put text like this into a page with CDP (Input.insertText) instead", r)
			}
		}
		for _, r := range text {
			if err := v.tap(nil, runeKeysym(r)); err != nil {
				return err
			}
		}
	case "key":
		if len(pos) == 0 {
			return errors.New("needs a key, e.g. Return or ctrl+l")
		}
		for _, combo := range pos {
			mods, key, err := parseCombo(combo)
			if err != nil {
				return err
			}
			if err := v.tap(mods, key); err != nil {
				return err
			}
		}
	default:
		return fmt.Errorf("unknown command\n\n%s", screenUsage)
	}
	return nil
}

// reorderFlags moves -flags ahead of positional arguments, so `click 10 20 -button
// right` works as well as `click -button right 10 20`. A lone "-" or a negative
// number is positional.
func reorderFlags(args []string) []string {
	var flags, rest []string
	for i := 0; i < len(args); i++ {
		a := args[i]
		if strings.HasPrefix(a, "-") && len(a) > 1 && (a[1] < '0' || a[1] > '9') {
			flags = append(flags, a)
			if !strings.Contains(a, "=") && a != "-double" && a != "--double" && i+1 < len(args) {
				flags = append(flags, args[i+1])
				i++
			}
			continue
		}
		rest = append(rest, a)
	}
	return append(flags, rest...)
}

func parseRect(s string) (image.Rectangle, error) {
	parts := strings.Split(s, ",")
	if len(parts) != 4 {
		return image.Rectangle{}, fmt.Errorf("crop %q is not X,Y,W,H", s)
	}
	var n [4]int
	for i, p := range parts {
		v, err := strconv.Atoi(strings.TrimSpace(p))
		if err != nil {
			return image.Rectangle{}, fmt.Errorf("crop %q is not X,Y,W,H", s)
		}
		n[i] = v
	}
	return image.Rect(n[0], n[1], n[0]+n[2], n[1]+n[3]), nil
}

type rfbConn struct {
	c    net.Conn
	r    *bufio.Reader
	w, h int
}

func dialRFB(addr string) (*rfbConn, error) {
	c, err := net.DialTimeout("tcp", addr, 5*time.Second)
	if err != nil {
		return nil, fmt.Errorf("%v (is cdp-unwrap running with -vnc-listen? see code-docker-chrome's install.sh)", err)
	}
	c.SetDeadline(time.Now().Add(30 * time.Second))
	v := &rfbConn{c: c, r: bufio.NewReader(c)}
	if err := v.handshake(); err != nil {
		c.Close()
		return nil, err
	}
	return v, nil
}

func (v *rfbConn) Close() error { return v.c.Close() }

func (v *rfbConn) handshake() error {
	version := make([]byte, 12)
	if _, err := io.ReadFull(v.r, version); err != nil {
		return fmt.Errorf("no VNC greeting (%v); unwrap logs say why the tunnel was refused", err)
	}
	if !strings.HasPrefix(string(version), "RFB 003.") {
		return fmt.Errorf("not a VNC server: %q", version)
	}
	if _, err := v.c.Write([]byte("RFB 003.008\n")); err != nil {
		return err
	}
	n, err := v.r.ReadByte()
	if err != nil {
		return err
	}
	if n == 0 {
		return fmt.Errorf("VNC refused: %s", v.readString())
	}
	types := make([]byte, n)
	if _, err := io.ReadFull(v.r, types); err != nil {
		return err
	}
	if !strings.ContainsRune(string(types), 1) {
		return errors.New("VNC requires a password (VNC_PASSWORD is set); screen only speaks no-auth VNC")
	}
	if _, err := v.c.Write([]byte{1}); err != nil {
		return err
	}
	var result uint32
	if err := binary.Read(v.r, binary.BigEndian, &result); err != nil {
		return err
	}
	if result != 0 {
		return fmt.Errorf("VNC security failed: %s", v.readString())
	}
	// shared=1: don't disconnect the person watching.
	if _, err := v.c.Write([]byte{1}); err != nil {
		return err
	}
	var init struct {
		W, H   uint16
		Format [16]byte
		Name   uint32
	}
	if err := binary.Read(v.r, binary.BigEndian, &init); err != nil {
		return err
	}
	if _, err := io.CopyN(io.Discard, v.r, int64(init.Name)); err != nil {
		return err
	}
	v.w, v.h = int(init.W), int(init.H)
	// 32 bpp, depth 24, little-endian, true colour, 8 bits each at shifts 16/8/0.
	format := []byte{0, 0, 0, 0, 32, 24, 0, 1, 0, 255, 0, 255, 0, 255, 16, 8, 0, 0, 0, 0}
	encodings := []byte{2, 0, 0, 1, 0, 0, 0, 0} // SetEncodings: Raw only
	_, err = v.c.Write(append(format, encodings...))
	return err
}

func (v *rfbConn) readString() string {
	var n uint32
	if binary.Read(v.r, binary.BigEndian, &n) != nil || n > 4096 {
		return "(no reason given)"
	}
	b := make([]byte, n)
	io.ReadFull(v.r, b)
	return string(b)
}

// screenshot returns the second full frame of this connection. wayvnc's first frame to
// a new client can predate the compositor's first render for it and come back as the
// bare background (measured: an empty grey screen while Chrome was up).
func (v *rfbConn) screenshot() (*image.RGBA, error) {
	img := image.NewRGBA(image.Rect(0, 0, v.w, v.h))
	if err := v.frame(img); err != nil {
		return nil, err
	}
	time.Sleep(400 * time.Millisecond)
	if err := v.frame(img); err != nil {
		return nil, err
	}
	return img, nil
}

func (v *rfbConn) frame(img *image.RGBA) error {
	return v.update(img, v.w, v.h)
}

// sync waits for a 1x1 update: a reply to a request sent after some events means
// wayvnc has read everything before it, so closing can't cut off the last event.
func (v *rfbConn) sync() error {
	return v.update(nil, 1, 1)
}

// update requests the w x h area at the origin and reads the reply into img (nil:
// discard).
func (v *rfbConn) update(img *image.RGBA, w, h int) error {
	req := []byte{3, 0, 0, 0, 0, 0, 0, 0, 0, 0}
	binary.BigEndian.PutUint16(req[6:], uint16(w))
	binary.BigEndian.PutUint16(req[8:], uint16(h))
	if _, err := v.c.Write(req); err != nil {
		return err
	}
	for {
		kind, err := v.r.ReadByte()
		if err != nil {
			return err
		}
		switch kind {
		case 0: // FramebufferUpdate
			var hdr struct {
				Pad   uint8
				Rects uint16
			}
			if err := binary.Read(v.r, binary.BigEndian, &hdr); err != nil {
				return err
			}
			for range int(hdr.Rects) {
				var r struct {
					X, Y, W, H uint16
					Encoding   int32
				}
				if err := binary.Read(v.r, binary.BigEndian, &r); err != nil {
					return err
				}
				if r.Encoding != 0 {
					return fmt.Errorf("server sent encoding %d, only Raw was asked for", r.Encoding)
				}
				row := make([]byte, int(r.W)*4)
				for y := range int(r.H) {
					if _, err := io.ReadFull(v.r, row); err != nil {
						return err
					}
					if img == nil {
						continue
					}
					py := int(r.Y) + y
					if py >= v.h {
						continue
					}
					for x := range int(r.W) {
						px := int(r.X) + x
						if px >= v.w {
							continue
						}
						// Little-endian pixel with red at bit 16: bytes are B, G, R, X.
						i := img.PixOffset(px, py)
						img.Pix[i+0] = row[x*4+2]
						img.Pix[i+1] = row[x*4+1]
						img.Pix[i+2] = row[x*4+0]
						img.Pix[i+3] = 255
					}
				}
			}
			return nil
		case 2: // Bell
		case 3: // ServerCutText
			if _, err := io.CopyN(io.Discard, v.r, 3); err != nil {
				return err
			}
			var n uint32
			if err := binary.Read(v.r, binary.BigEndian, &n); err != nil {
				return err
			}
			if _, err := io.CopyN(io.Discard, v.r, int64(n)); err != nil {
				return err
			}
		default:
			return fmt.Errorf("unexpected server message %d", kind)
		}
	}
}

func (v *rfbConn) pointer(mask uint8, x, y int) error {
	x = max(0, min(x, v.w-1))
	y = max(0, min(y, v.h-1))
	msg := []byte{5, mask, 0, 0, 0, 0}
	binary.BigEndian.PutUint16(msg[2:], uint16(x))
	binary.BigEndian.PutUint16(msg[4:], uint16(y))
	_, err := v.c.Write(msg)
	return err
}

func (v *rfbConn) pressRelease(mask uint8, x, y int) error {
	if err := v.pointer(mask, x, y); err != nil {
		return err
	}
	time.Sleep(40 * time.Millisecond)
	if err := v.pointer(0, x, y); err != nil {
		return err
	}
	time.Sleep(40 * time.Millisecond)
	return nil
}

func (v *rfbConn) key(down bool, sym uint32) error {
	msg := []byte{4, 0, 0, 0, 0, 0, 0, 0}
	if down {
		msg[1] = 1
	}
	binary.BigEndian.PutUint32(msg[4:], sym)
	_, err := v.c.Write(msg)
	return err
}

func (v *rfbConn) tap(mods []uint32, sym uint32) error {
	for _, m := range mods {
		if err := v.key(true, m); err != nil {
			return err
		}
	}
	if err := v.key(true, sym); err != nil {
		return err
	}
	if err := v.key(false, sym); err != nil {
		return err
	}
	for i := len(mods) - 1; i >= 0; i-- {
		if err := v.key(false, mods[i]); err != nil {
			return err
		}
	}
	time.Sleep(15 * time.Millisecond)
	return nil
}

var modifierKeysyms = map[string]uint32{
	"ctrl": 0xffe3, "control": 0xffe3, "shift": 0xffe1, "alt": 0xffe9,
	"super": 0xffeb, "win": 0xffeb, "meta": 0xffeb,
}

var namedKeysyms = map[string]uint32{
	"return": 0xff0d, "enter": 0xff0d, "escape": 0xff1b, "esc": 0xff1b, "tab": 0xff09,
	"backspace": 0xff08, "delete": 0xffff, "insert": 0xff63, "home": 0xff50, "end": 0xff57,
	"left": 0xff51, "up": 0xff52, "right": 0xff53, "down": 0xff54,
	"pageup": 0xff55, "pagedown": 0xff56, "space": 0x20, "menu": 0xff67,
}

func parseCombo(combo string) ([]uint32, uint32, error) {
	parts := strings.Split(combo, "+")
	var mods []uint32
	for _, m := range parts[:len(parts)-1] {
		sym, ok := modifierKeysyms[strings.ToLower(m)]
		if !ok {
			return nil, 0, fmt.Errorf("unknown modifier %q in %q", m, combo)
		}
		mods = append(mods, sym)
	}
	name := parts[len(parts)-1]
	if sym, ok := namedKeysyms[strings.ToLower(name)]; ok {
		return mods, sym, nil
	}
	if n, err := strconv.Atoi(strings.TrimPrefix(strings.ToLower(name), "f")); err == nil && len(name) > 1 && (name[0] == 'f' || name[0] == 'F') && n >= 1 && n <= 12 {
		return mods, 0xffbe + uint32(n-1), nil
	}
	if utf8.RuneCountInString(name) == 1 {
		r, _ := utf8.DecodeRuneInString(name)
		return mods, runeKeysym(r), nil
	}
	return nil, 0, fmt.Errorf("unknown key %q", name)
}

func runeKeysym(r rune) uint32 {
	switch {
	case r == '\n':
		return 0xff0d
	case r == '\t':
		return 0xff09
	case r < 0x100:
		return uint32(r)
	default:
		return 0x01000000 | uint32(r)
	}
}
