package main

import (
	"log"
	"os/exec"
	"fmt"

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
var focused xproto.Window

// bar
var barWindow xproto.Window
var barGC xproto.Gcontext
var barHeight uint16 = 20 // bar height in pixels


func main() {
	conn, err := xgb.NewConn()
	if err != nil {
		log.Fatal(err)
	}

	setup(conn)

	for {
		ev, err := conn.WaitForEvent()
		if err != nil {
			log.Fatal(err)
		}
		//	event loop
		switch e := ev.(type) {
		case xproto.KeyPressEvent:
			handleKeyPress(conn, e)
		case xproto.MapRequestEvent:
			manageWindow(conn, e.Window)
		case xproto.DestroyNotifyEvent:
			unmanageWindow(conn, e.Window)
		case xproto.UnmapNotifyEvent:
			unmanageWindow(conn, e.Window)
		case xproto.EnterNotifyEvent:
			focused = e.Event
			setInputFocus(conn, focused)
			// update bar
			title := getWindowTitle(conn, focused)
			drawBarText(conn, fmt.Sprintf("[%s]", title))
		case xproto.ExposeEvent:
			if e.Window == barWindow {
				title := getWindowTitle(conn, focused)
				if title == "None" {
					title = "wm"
				}
				drawBarText(conn, fmt.Sprintf("[%s]", title))
				
			}
		default:
			log.Println("event: ", e)
		}

	}
}

func setup(conn *xgb.Conn) {
	log.Println("setup")
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
	createBar(conn)
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

func killActiveWindow(conn *xgb.Conn, win xproto.Window) {
	if win == 0 {
		log.Println("no focused window to kill")
		return
	}

	// try to use WM_DELETE_WINDOW to close
	wmProtocols, err := xproto.InternAtom(conn, true,
		uint16(len("WM_PROTOCOLS")), "WM_PROTOCOLS").Reply()
	if err != nil {
		log.Println("failed to get WM_PROTOCOLS atom", err)
		return
	}

	wmDelete, err := xproto.InternAtom(conn, true,
		uint16(len("WM_DELETE_WINDOW")), "WM_DELETE_WINDOW").Reply()
	if err != nil {
		log.Println("failed to get WM_DELETE_WINDOW atom", err)
		return
	}

	data := xproto.ClientMessageDataUnionData32New([]uint32{
		uint32(wmDelete.Atom), uint32(xproto.TimeCurrentTime), 0, 0, 0,
	})

	ev := xproto.ClientMessageEvent{
		Format: 32,
		Window: win,
		Type:   wmProtocols.Atom,
		Data:   data,
	}

	// send event
	err = xproto.SendEventChecked(conn, false, win, xproto.EventMaskNoEvent, string(ev.Bytes())).Check()
	if err != nil {
		// force close if WM_DELETE_WINDOW doesnt work
		log.Println("force closing:", err)
		xproto.DestroyWindow(conn, win)
	}

	delete(clients, win)
	focused = 0
	tile(conn)
}

func setupKeys(conn *xgb.Conn) {
	mod := uint16(xproto.ModMask1) 

	// keybinds
	keys = []Key{
		{
			Mod: mod,
			Sym: keysym.XK_Return,
			Fn: func(conn *xgb.Conn) {
				spawn("st")
			},
		},
		{
			Mod: mod,
			Sym: keysym.XK_q,
			Fn: func(conn *xgb.Conn) {
				killActiveWindow(conn, focused)
			},
		},
		{
			Mod: mod,
			Sym: keysym.XK_d,
			Fn: func(conn *xgb.Conn) {
				spawn("dmenu_launch")
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

        ignoredMods := []uint16{
            0,
            xproto.ModMaskLock,
            xproto.ModMask2,
            xproto.ModMaskLock | xproto.ModMask2,
        }

        for _, m := range ignoredMods {
            xproto.GrabKey(conn,
                true,
                root,
                k.Mod|m,
                keycode,
                xproto.GrabModeAsync,
                xproto.GrabModeAsync,
            )
        }
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
	reply, err := xproto.GetKeyboardMapping(conn, e.Detail, 1).Reply()
	if err != nil || len(reply.Keysyms) == 0 {
		return
	}

	log.Println(reply)

	sym := reply.Keysyms[0]

	for _, k := range keys {
		if uint32(sym) != k.Sym {
			continue
		}

		ignoredMods := uint16(xproto.ModMaskLock) | uint16(xproto.ModMask2)
		modifiers := e.State &^ ignoredMods

		if modifiers == k.Mod {
			k.Fn(conn)
		}
	}
}

func setInputFocus(conn *xgb.Conn, win xproto.Window) {
	if win == 0 {
		return
	}
	xproto.SetInputFocus(conn, xproto.InputFocusPointerRoot, win, xproto.TimeCurrentTime)
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

	focused = win
	setInputFocus(conn, focused)

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
    
    usableHeight := int(screen.HeightInPixels) - int(barHeight)

    n := len(clients)
    if n == 0 {
        return
    }

    heightPerWin := usableHeight / n

    i := 0
    for _, c := range clients {
        yOffset := int(barHeight) + (i * heightPerWin)

        xproto.ConfigureWindow(conn, c.Win,
            xproto.ConfigWindowX|
                xproto.ConfigWindowY|
                xproto.ConfigWindowWidth|
                xproto.ConfigWindowHeight,
            []uint32{
                0, // x
                uint32(yOffset), // y
                uint32(screenWidth),
                uint32(heightPerWin),
            })

        i++
    }
}

func createBar(conn *xgb.Conn) {
	screen := xproto.Setup(conn).DefaultScreen(conn)
	root := screen.Root

	// create the window
	cw := []uint32{
		xproto.EventMaskExposure, // redraw on expose
		uint32(0xFFFFFF), // white background
	}

	barWindow, _ = xproto.NewWindowId(conn)
	xproto.CreateWindow(
		conn,
		screen.RootDepth,
		barWindow,
		root,
		0, 0, // x, y
		screen.WidthInPixels, barHeight,
		0, // border
		xproto.WindowClassInputOutput,
		screen.RootVisual,
		xproto.CwBackPixel|xproto.CwEventMask,
		cw,
	)

	// map bar window
	xproto.MapWindow(conn, barWindow)

	// create bar gc
	barGC, _ = xproto.NewGcontextId(conn)
	xproto.CreateGC(conn, barGC, xproto.Drawable(barWindow), xproto.GcForeground|xproto.GcBackground, []uint32{0x000000, 0xFFFFFF})
}

func drawBarText(conn *xgb.Conn, text string) {
	if text == "" {
		text = " "
	}

	screen := xproto.Setup(conn).DefaultScreen(conn)
	xproto.ClearArea(conn, false, barWindow, 0, 0, screen.WidthInPixels, barHeight)

	// ascii only because FUCK POLYTEXT8
	ascii := make([]byte, 0, len(text))
	for i := 0; i < len(text); i++ {
		if text[i] >= 32 && text[i] <= 126 {
			ascii = append(ascii, text[i])
		}
	}
	if len(ascii) == 0 {
		ascii = []byte(" ")
	}

	textItem := make([]byte, len(ascii)+2)	
    textItem[0] = byte(len(ascii)) // string len
    textItem[1] = 0 // offset
    copy(textItem[2:], ascii)

    err := xproto.PolyText8Checked(conn, xproto.Drawable(barWindow), barGC, 10, int16(barHeight-5), textItem).Check()
    if err != nil {
        log.Println("polytext failed again :D err:", err)
    }
}

func getWindowTitle(conn *xgb.Conn, win xproto.Window) string {
	if focused == 0 {
		return "None"
	}

	prop, err := xproto.GetProperty(conn, false, focused, xproto.AtomWmName, xproto.AtomString, 0, 100).Reply()
	
	if err != nil {
		log.Println("failed to get window title:", err)
		return "Unknown"
	}

	if prop == nil || len(prop.Value) == 0 {
		return "Unnamed"
	}

	return string(prop.Value)
}