package main

import (
	"log"
	"os"
	"os/exec"

	"github.com/BurntSushi/xgb"
	"github.com/BurntSushi/xgb/xproto"

	"github.com/crecheer/wm/keysym"
)

// structs

type Key struct {
	Mod uint16
	Sym uint32
	Fn  func(*xgb.Conn)
}

type Client struct {
	Win      xproto.Window
	X        int16
	Y        int16
	Width    uint16
	Height   uint16
	Floating bool
}

var keys []Key
var clients = make(map[xproto.Window]*Client)

func main() {
	conn, err := xgb.NewConn()
	if err != nil {
		log.Fatal(err)
	}

	setup(conn)

	for {
		log.Println("test")
		ev, err := conn.WaitForEvent()
		if err != nil {
			log.Fatal(err)
		}

		switch e := ev.(type) {
		case xproto.KeyPressEvent:
			handleKeyPress(conn, e)
		
		case xproto.MapRequestEvent:
			manageWindow(conn, e.Window)

		case xproto.DestroyNotifyEvent:
			unmanageWindow(conn, e.Window)

		case xproto.UnmapNotifyEvent:
			unmanageWindow(conn, e.Window)
		
		default:
			log.Println("event: ", e)
		}

	}
}

func setup(conn *xgb.Conn) {
	root := xproto.Setup(conn).DefaultScreen(conn).Root

	err := xproto.ChangeWindowAttributesChecked(
		conn,
		root,
		xproto.CwEventMask,
		[]uint32{
			xproto.EventMaskSubstructureRedirect |
				xproto.EventMaskSubstructureNotify,
		},
	).Check()

	if err != nil {
		log.Fatal(err)
	}

	setupKeys(conn)
}

func spawn(cmd string, args ...string) {
	c := exec.Command(cmd, args...)
	c.Stdout = nil
	c.Stderr = nil
	c.Stdin = nil
	if err := c.Start(); err != nil {
		log.Println("failed to spawn", cmd, ":", err)
	}
}

func setupKeys(conn *xgb.Conn) {
	mod := uint16(xproto.ModMask1) // Alt

	keys = []Key{
		{
			Mod: mod,
			Sym: keysym.XK_Return,
			Fn: func(c *xgb.Conn) {
				spawn("st")
			},
		},
		{
			Mod: mod,
			Sym: keysym.XK_Q,
			Fn: func(c *xgb.Conn) {
				os.Exit(0)
			},
		},
	}

	grabKeys(conn)
}

func grabKeys(conn *xgb.Conn) {
	root := xproto.Setup(conn).DefaultScreen(conn).Root

	for _, k := range keys {
		keycode := keysymToKeycode(conn, k.Sym)
		if keycode == 0 {
			continue
		}

		xproto.GrabKey(conn,
			true,
			root,
			k.Mod,
			keycode,
			xproto.GrabModeAsync,
			xproto.GrabModeAsync,
		)
	}
}

func keysymToKeycode(conn *xgb.Conn, sym uint32) xproto.Keycode {
	setup := xproto.Setup(conn)
	min := setup.MinKeycode
	max := setup.MaxKeycode

	reply, err := xproto.GetKeyboardMapping(conn, min, byte(max-min+1)).Reply()
	if err != nil {
		return 0
	}

	keysymsPerKeycode := int(reply.KeysymsPerKeycode)

	for kc := min; kc <= max; kc++ {
		for i := 0; i < keysymsPerKeycode; i++ {
			idx := int(kc-min)*keysymsPerKeycode + i
			if uint32(reply.Keysyms[idx]) == sym {
				return kc
			}
		}
	}

	return 0
}

func handleKeyPress(conn *xgb.Conn, e xproto.KeyPressEvent) {
	log.Println("key pressed")
	reply, err := xproto.GetKeyboardMapping(conn, e.Detail, 1).Reply()
	if err != nil || len(reply.Keysyms) == 0 {
		return
	}

	

	sym := reply.Keysyms[0]

	for _, k := range keys {
		if uint32(sym) == k.Sym && (e.State&k.Mod) == k.Mod {
    		k.Fn(conn)
		}
	}
}

func manageWindow(conn *xgb.Conn, win xproto.Window) {
	geom, err := xproto.GetGeometry(conn, xproto.Drawable(win)).Reply()
	if err != nil {
		return
	}

	client := &Client{
		Win:      win,
		X:        geom.X,
		Y:        geom.Y,
		Width:    geom.Width,
		Height:   geom.Height,
		Floating: false,
	}

	clients[win] = client
	log.Println("managing window:", win)

	// listen for events on this window
	xproto.ChangeWindowAttributes(conn, win,
		xproto.CwEventMask,
		[]uint32{
			xproto.EventMaskEnterWindow |
				xproto.EventMaskFocusChange |
				xproto.EventMaskPropertyChange,
		})

	// map (show) window
	xproto.MapWindow(conn, win)

	// tile all windows
	tile(conn)
}

func unmanageWindow(conn *xgb.Conn, win xproto.Window) {
	if _, ok := clients[win]; ok {
		log.Println("unmanaging window:", win)
		delete(clients, win)
		tile(conn)
	}
}

func tile(conn *xgb.Conn) {
	log.Println("tile")
	screen := xproto.Setup(conn).DefaultScreen(conn)
	screenWidth := int(screen.WidthInPixels)
	screenHeight := int(screen.HeightInPixels)

	n := len(clients)
	if n == 0 {
		return
	}

	i := 0
	for _, c := range clients {
		height := screenHeight / n

		xproto.ConfigureWindow(conn, c.Win,
			xproto.ConfigWindowX|
				xproto.ConfigWindowY|
				xproto.ConfigWindowWidth|
				xproto.ConfigWindowHeight,
			[]uint32{
				0,
				uint32(i * height),
				uint32(screenWidth),
				uint32(height),
			})

		i++
	}
}
