package main

import (
	"fmt"
	"image"
	"strings"

	"gioui.org/io/pointer"
	"gioui.org/layout"
	"gioui.org/op/clip"
	"gioui.org/text"
	"gioui.org/widget"
	"gioui.org/widget/material"
)

func (a *App) LayoutDRO(gtx C) D {
	return layout.UniformInset(5).Layout(gtx, func(gtx C) D {
		return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
			layout.Rigid(a.LayoutGrblStatus),
			layout.Rigid(layout.Spacer{Height: 5}.Layout),
			layout.Rigid(a.LayoutDROCoords),
			layout.Rigid(layout.Spacer{Height: 5}.Layout),
			layout.Rigid(a.LayoutFeedSpeed),
			layout.Rigid(layout.Spacer{Height: 5}.Layout),
			layout.Rigid(a.LayoutGCodes),
			layout.Rigid(func(gtx C) D {
				return drawGrblModes(a.th, gtx, a.g)
			}),
		)
	})
}

func (a *App) LayoutGrblStatus(gtx C) D {
	label := material.H4(a.th, strings.ToUpper(a.g.Status))
	label.Alignment = text.Middle
	borderColour := grey(128)
	return widget.Border{Width: 1, CornerRadius: 2, Color: borderColour}.Layout(gtx, func(gtx C) D {
		return LayoutColour(gtx, grey(32), func(gtx C) D {
			return layout.UniformInset(5).Layout(gtx, func(gtx C) D {
				return label.Layout(gtx)
			})
		})
	})
}

func (a *App) LayoutDROCoords(gtx C) D {
	if a.g.Vel.Length() > 0.001 {
		// invalidate the frame if the velocity is non-zero,
		// because we need to redraw the extrapolated coordinates
		a.w.Invalidate()
	}

	return Panel{Width: 1, Color: grey(128), CornerRadius: 5, Padding: 5, BackgroundColor: grey(32)}.Layout(gtx, func(gtx C) D {
		return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
			layout.Rigid(func(gtx C) D {
				return a.LayoutDROCoord(gtx, "X", a.g.WposExt().X)
			}),
			layout.Rigid(func(gtx C) D {
				return a.LayoutDROCoord(gtx, "Y", a.g.WposExt().Y)
			}),
			layout.Rigid(func(gtx C) D {
				return a.LayoutDROCoord(gtx, "Z", a.g.WposExt().Z)
			}),
			// TODO: optional 4th axis?
		)
	})
}

func (a *App) ShowDROEditor(axis string, initVal float64) {
	a.ShowNumPop(axis, initVal, func(val float64) {
		a.g.SetWpos(axis, val)
	})
}

func (a *App) LayoutDROCoord(gtx C, name string, val float64) D {
	for _, gtxEvent := range gtx.Events(name) {
		switch gtxE := gtxEvent.(type) {
		case pointer.Event:
			if gtxE.Kind == pointer.Press {
				if name == "X" {
					a.ShowDROEditor("X", a.g.Wpos.X)
				} else if name == "Y" {
					a.ShowDROEditor("Y", a.g.Wpos.Y)
				} else if name == "Z" {
					a.ShowDROEditor("Z", a.g.Wpos.Z)
				} else if name == "A" {
					a.ShowDROEditor("A", a.g.Wpos.A)
				}
			}
		}
	}

	readout := Readout{th: a.th, decimalPlaces: 3, TextSize: material.H4(a.th, "").TextSize, BackgroundColor: grey(0)}
	dims := readout.Layout(gtx, name, val)

	defer clip.Rect(image.Rectangle{Max: dims.Size}).Push(gtx.Ops).Pop()
	pointer.InputOp{
		Kinds: pointer.Press,
		Tag:   name, // TODO: a better tag?
	}.Add(gtx.Ops)

	if a.mode == ModeNum && a.numpop != nil && a.numpopType == name {
		gtx.Constraints.Max.X = dims.Size.X - 50
		a.numpop.Layout(gtx, image.Pt(50, dims.Size.Y))
	}

	return dims
}

func (a *App) LayoutFeedSpeed(gtx C) D {
	readout := Readout{th: a.th, TextSize: material.H5(a.th, "").TextSize, BackgroundColor: grey(0)}

	return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
		layout.Rigid(func(gtx C) D {
			return readout.Layout(gtx, " Feed", a.g.FeedRate)
		}),
		layout.Rigid(func(gtx C) D {
			return readout.Layout(gtx, "Speed", a.g.SpindleSpeed)
		}),
	)
}

func (a *App) LayoutGCodes(gtx C) D {
	label := material.H6(a.th, fmt.Sprintf(a.g.GCodes))
	return label.Layout(gtx)
}

func drawGrblModes(th *material.Theme, gtx C, g *Grbl) D {
	probeStr := ""
	if g.Probe {
		probeStr = "[probe]"
	}
	label := material.H5(th, probeStr)
	return label.Layout(gtx)
}
