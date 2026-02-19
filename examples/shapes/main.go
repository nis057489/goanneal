package main

import (
	"container/heap"
	"fmt"
	"image/color"
	"log"
	"math"
	"math/rand"
	"sort"
	"time"

	"github.com/hajimehoshi/ebiten/v2"
	"github.com/hajimehoshi/ebiten/v2/ebitenutil"
)

type Game struct {
	state          *TileState
	temp           float64
	initialTemp    float64
	minTemp        float64
	coolRate       float64
	tileSize       int
	steps          int
	accepts        int
	improves       int
	bestCost       float64
	lastPrint      float64
	repairCounter  int
	bestState      *TileState
	movesPerUpdate int
	seenStates     map[string]int
	lastImprove    int
	stagnationMax  int
	archive        map[int][]*ArchivedState
	archiveCap     int
}

type ArchivedState struct {
	state  *TileState
	energy float64
	step   int
}

func (g *Game) Update() error {
	if g.state.Done {
		if g.steps > 0 {
			// Print results once
			g.state.PrintBestPositions(3)
			g.steps = 0
		}
		return nil
	}

	if g.temp > g.minTemp {
		// Do multiple moves per update
		for i := 0; i < g.movesPerUpdate; i++ {
			g.steps++
			g.repairCounter++
			currentEnergy := g.state.Energy()
			currentOverlap := g.state.OverlapCount()

			if g.steps == 1 {
				g.bestCost = currentEnergy
				g.bestState = g.state.Copy().(*TileState)
				fmt.Printf("\nStep\tTemp\t\tEnergy\t\tAccepts\tImproves\tBest\n")
			}

			// Only check repairs every 10000 steps
			if g.repairCounter >= 10000 {
				g.repairCounter = 0
				g.state.RepairConstraints()
				continue
			}

			// Snapshot state for rollback
			prevState := g.state.Copy().(*TileState)
			prevStateKey := g.state.MemoKey()
			prevEnergy := currentEnergy

			// Regular annealing move
			g.state.Move()
			newStateKey := g.state.MemoKey()
			if newStateKey == prevStateKey {
				if g.state.forcePerturb() {
					newStateKey = g.state.MemoKey()
				}
			}
			if newStateKey == prevStateKey {
				g.state = prevState
				continue
			}
			maxVisits := g.maxVisitsForTemp()
			newOverlap := g.state.OverlapCount()
			if newOverlap > 0 {
				maxVisits = 1_000_000
			}
			if g.seenStates[newStateKey] >= maxVisits {
				g.state = prevState
				continue
			}
			newEnergy := g.state.Energy()
			delta := newEnergy - prevEnergy

			shouldReject := false
			if newEnergy >= hardInvalidEnergy {
				shouldReject = true
			} else if newOverlap < currentOverlap {
				shouldReject = false
			} else if delta > 0 && math.Exp(-delta/g.temp) < rand.Float64() {
				shouldReject = true
			}

			if shouldReject {
				g.state = prevState
			} else {
				g.seenStates[newStateKey]++
				g.accepts++
				if g.accepts%40 == 0 {
					g.storeArchiveState(newEnergy)
				}
				if newEnergy < g.bestCost {
					g.improves++
					g.bestCost = newEnergy
					g.bestState = g.state.Copy().(*TileState)
					g.lastImprove = g.steps
					g.storeArchiveState(newEnergy)
				}
			}

			if g.steps-g.lastImprove > g.stagnationMax {
				if g.state.OverlapCount() == 0 {
					if g.jumpFromArchive() {
						g.lastImprove = g.steps
						g.temp = math.Min(g.initialTemp, g.temp*1.06)
					} else {
						g.lastImprove = g.steps
					}
				} else {
					g.temp = math.Min(g.initialTemp, g.temp*1.03)
					g.lastImprove = g.steps
				}
			}
		}

		// Print progress only once per visual update
		if float64(g.steps)-g.lastPrint >= 100 {
			fmt.Printf("%d\t%.6f\t%.2f\t\t%.2f%%\t%.2f%%\t%.2f\n",
				g.steps,
				g.temp,
				g.state.Energy(),
				100.0*float64(g.accepts)/float64(g.steps),
				100.0*float64(g.improves)/float64(g.steps),
				g.bestCost)
			g.lastPrint = float64(g.steps)
		}

		g.temp *= g.coolRate
	} else {
		g.state.Done = true
	}
	return nil
}

func (g *Game) maxVisitsForTemp() int {
	ratio := g.temp / g.initialTemp
	switch {
	case ratio > 0.7:
		return 24
	case ratio > 0.4:
		return 16
	case ratio > 0.2:
		return 10
	default:
		return 6
	}
}

func (g *Game) tempLevel() int {
	ratio := g.temp / g.initialTemp
	switch {
	case ratio > 0.7:
		return 0
	case ratio > 0.4:
		return 1
	case ratio > 0.2:
		return 2
	default:
		return 3
	}
}

func (g *Game) storeArchiveState(energy float64) {
	if g.archive == nil {
		g.archive = make(map[int][]*ArchivedState)
	}

	level := g.tempLevel()
	entry := &ArchivedState{
		state:  g.state.Copy().(*TileState),
		energy: energy,
		step:   g.steps,
	}

	g.archive[level] = append(g.archive[level], entry)
	sort.Slice(g.archive[level], func(i, j int) bool {
		if g.archive[level][i].energy == g.archive[level][j].energy {
			return g.archive[level][i].step > g.archive[level][j].step
		}
		return g.archive[level][i].energy < g.archive[level][j].energy
	})

	if len(g.archive[level]) > g.archiveCap {
		g.archive[level] = g.archive[level][:g.archiveCap]
	}
}

func (g *Game) jumpFromArchive() bool {
	levelOrder := []int{g.tempLevel(), g.tempLevel() - 1, g.tempLevel() + 1, 0, 1, 2, 3}
	for _, level := range levelOrder {
		if level < 0 || level > 3 {
			continue
		}
		entries := g.archive[level]
		if len(entries) == 0 {
			continue
		}
		pickTop := len(entries)
		if pickTop > 5 {
			pickTop = 5
		}
		choice := entries[rand.Intn(pickTop)]
		g.state = choice.state.Copy().(*TileState)
		g.seenStates[g.state.MemoKey()]++
		return true
	}
	return false
}

func (g *Game) Draw(screen *ebiten.Image) {
	drawerFill := color.RGBA{38, 28, 20, 255}
	drawerBorder := color.RGBA{170, 120, 75, 255}
	ebitenutil.DrawRect(screen, 0, 0, float64(g.state.Grid.Width*g.tileSize), float64(g.state.Grid.Height*g.tileSize), drawerFill)
	ebitenutil.DrawRect(screen, 0, 0, float64(g.state.Grid.Width*g.tileSize), 4, drawerBorder)
	ebitenutil.DrawRect(screen, 0, float64(g.state.Grid.Height*g.tileSize-4), float64(g.state.Grid.Width*g.tileSize), 4, drawerBorder)
	ebitenutil.DrawRect(screen, 0, 0, 4, float64(g.state.Grid.Height*g.tileSize), drawerBorder)
	ebitenutil.DrawRect(screen, float64(g.state.Grid.Width*g.tileSize-4), 0, 4, float64(g.state.Grid.Height*g.tileSize), drawerBorder)

	for _, objectTiles := range groupObjects(g.state.Grid.Tiles) {
		rep := objectTiles[0]
		if rep.Constrained {
			continue
		}

		minX, minY, maxX, maxY := objectBounds(objectTiles)
		x := float64(minX * g.tileSize)
		y := float64(minY * g.tileSize)
		w := float64((maxX-minX+1)*g.tileSize)
		h := float64((maxY-minY+1)*g.tileSize)

		drawClutterShape(screen, rep.Shape, x, y, w, h, getTileColor(rep.Color), rep.Displaced)
	}

	if !g.state.Done {
		px := float64(g.state.Polygon.PosX * g.tileSize)
		py := float64(g.state.Polygon.PosY * g.tileSize)
		pw := float64(g.state.Polygon.Width * g.tileSize)
		ph := float64(g.state.Polygon.Height * g.tileSize)
		drawIncomingObject(screen, px, py, pw, ph)
	}

	// Draw debug text
	if g.state.Done {
		msg := "No valid positions found!"
		if g.state.bestPosition != nil {
			msg = fmt.Sprintf("Best position found! Objects to move score: %.0f",
				g.state.bestPosition.Energy/1000.0)
		}
		ebitenutil.DebugPrint(screen, msg)
	}
}

func getTileColor(c Color) color.Color {
	switch c {
	case Red:
		return color.RGBA{255, 0, 0, 255}
	case Blue:
		return color.RGBA{0, 0, 255, 255}
	case Green:
		return color.RGBA{0, 255, 0, 255}
	case Yellow:
		return color.RGBA{255, 255, 0, 255}
	default:
		return color.White
	}
}

func (g *Game) Layout(w, h int) (int, int) {
	return g.state.Grid.Width * g.tileSize, g.state.Grid.Height * g.tileSize
}

func main() {
	rand.Seed(time.Now().UnixNano())

	grid := NewGrid(20, 20)

	// Drawer walls (constrained)
	for x := 0; x < grid.Width; x++ {
		grid.AddTile(x, 0, Red, true)
		grid.AddTile(x, grid.Height-1, Red, true)
	}
	for y := 1; y < grid.Height-1; y++ {
		grid.AddTile(0, y, Red, true)
		grid.AddTile(grid.Width-1, y, Red, true)
	}

	// Cluttered drawer objects: stars, squares, cylinders, rectangles
	shapeDefs := []struct {
		name  string
		mask  [][]bool
		color Color
		kind  ShapeKind
		count int
	}{
		{name: "star", mask: starMask(), color: Yellow, kind: ShapeStar, count: 4},
		{name: "square", mask: squareMask(3), color: Blue, kind: ShapeSquare, count: 4},
		{name: "cylinder", mask: cylinderMask(), color: Green, kind: ShapeCylinder, count: 3},
		{name: "rectangle", mask: rectangleMask(5, 2), color: Blue, kind: ShapeRectangle, count: 4},
	}

	for _, def := range shapeDefs {
		for i := 0; i < def.count; i++ {
			placed := false
			for tries := 0; tries < 1400; tries++ {
				maskW, maskH := maskDimensions(def.mask)
				x := 1 + rand.Intn(grid.Width-maskW-2)
				y := 1 + rand.Intn(grid.Height-maskH-2)
				if grid.AddObjectFromMask(def.mask, x, y, def.color, def.kind, false) {
					placed = true
					break
				}
			}
			if !placed {
				fmt.Printf("warning: could not place %s object %d\n", def.name, i+1)
			}
		}
	}

	movableCells := 0
	for _, t := range grid.Tiles {
		if !t.Constrained {
			movableCells++
		}
	}
	maxMovableForGuaranteed9x9 := (grid.Width-2)*(grid.Height-2) - (9 * 9)
	if movableCells > maxMovableForGuaranteed9x9 {
		fmt.Printf("warning: clutter density is high (%d cells), fitting 9x9 may require major rearrangement\n", movableCells)
	}

	// New object we want to place in the cluttered drawer
	polygon := NewRectangle(9, 9, Blue)
	polygon.PosX = 1
	polygon.PosY = 1

	state := &TileState{
		Grid:           grid,
		Polygon:        polygon,
		validPositions: make(ValidPositionQueue, 0),
	}
	heap.Init(&state.validPositions)

	// Validate initial position for incoming 9x9 object
	if !state.isValidPosition() {
		found := false
		for x := 1; x < grid.Width-polygon.Width-1; x++ {
			for y := 1; y < grid.Height-polygon.Height-1; y++ {
				polygon.PosX = x
				polygon.PosY = y
				if state.isValidPosition() {
					found = true
					break
				}
			}
			if found {
				break
			}
		}
		if !found {
			log.Fatal("Could not find valid starting position for 9x9 object")
		}
	}

	game := &Game{
		state:          state,
		temp:           120.0,
		initialTemp:    120.0,
		minTemp:        0.8,
		coolRate:       0.9992,
		tileSize:       30,
		bestCost:       math.MaxFloat64,
		repairCounter:  0,
		bestState:      state.Copy().(*TileState),
		movesPerUpdate: 140,
		seenStates:     map[string]int{state.MemoKey(): 1},
		lastImprove:    0,
		stagnationMax:  1800,
		archive:        make(map[int][]*ArchivedState),
		archiveCap:     20,
	}
	game.storeArchiveState(game.state.Energy())

	ebiten.SetWindowSize(600, 600)
	ebiten.SetWindowTitle("Cluttered Drawer Annealing")

	if err := ebiten.RunGame(game); err != nil {
		log.Fatal(err)
	}
}

func groupObjects(tiles []*Tile) [][]*Tile {
	byObject := make(map[int][]*Tile)
	for _, tile := range tiles {
		byObject[tile.ObjectID] = append(byObject[tile.ObjectID], tile)
	}

	ids := make([]int, 0, len(byObject))
	for id := range byObject {
		ids = append(ids, id)
	}
	sort.Ints(ids)

	groups := make([][]*Tile, 0, len(ids))
	for _, id := range ids {
		groups = append(groups, byObject[id])
	}
	return groups
}

func objectBounds(tiles []*Tile) (int, int, int, int) {
	minX, minY := tiles[0].X, tiles[0].Y
	maxX, maxY := tiles[0].X, tiles[0].Y
	for _, tile := range tiles[1:] {
		if tile.X < minX {
			minX = tile.X
		}
		if tile.Y < minY {
			minY = tile.Y
		}
		if tile.X > maxX {
			maxX = tile.X
		}
		if tile.Y > maxY {
			maxY = tile.Y
		}
	}
	return minX, minY, maxX, maxY
}

func drawClutterShape(screen *ebiten.Image, kind ShapeKind, x, y, w, h float64, c color.Color, displaced bool) {
	base := c
	if displaced {
		base = color.RGBA{255, 220, 120, 255}
	}

	pad := 3.0
	sx, sy := x+pad, y+pad
	sw, sh := w-2*pad, h-2*pad

	switch kind {
	case ShapeStar:
		drawStar(screen, sx, sy, sw, sh, base)
	case ShapeCylinder:
		drawCylinder(screen, sx, sy, sw, sh, base)
	case ShapeRectangle:
		ebitenutil.DrawRect(screen, sx, sy+sh*0.2, sw, sh*0.6, base)
		ebitenutil.DrawLine(screen, sx, sy+sh*0.2, sx+sw, sy+sh*0.2, color.Black)
		ebitenutil.DrawLine(screen, sx, sy+sh*0.8, sx+sw, sy+sh*0.8, color.Black)
	default:
		ebitenutil.DrawRect(screen, sx, sy, sw, sh, base)
	}
}

func drawIncomingObject(screen *ebiten.Image, x, y, w, h float64) {
	ebitenutil.DrawRect(screen, x+2, y+2, w-4, h-4, color.RGBA{170, 90, 210, 120})
	ebitenutil.DrawLine(screen, x+2, y+2, x+w-2, y+2, color.RGBA{235, 180, 255, 255})
	ebitenutil.DrawLine(screen, x+w-2, y+2, x+w-2, y+h-2, color.RGBA{235, 180, 255, 255})
	ebitenutil.DrawLine(screen, x+w-2, y+h-2, x+2, y+h-2, color.RGBA{235, 180, 255, 255})
	ebitenutil.DrawLine(screen, x+2, y+h-2, x+2, y+2, color.RGBA{235, 180, 255, 255})
}

func drawStar(screen *ebiten.Image, x, y, w, h float64, c color.Color) {
	cx := x + w/2
	cy := y + h/2
	rOuter := math.Min(w, h) * 0.5
	rInner := rOuter * 0.42
	points := make([][2]float64, 10)
	for i := 0; i < 10; i++ {
		ang := -math.Pi/2 + float64(i)*math.Pi/5
		r := rOuter
		if i%2 == 1 {
			r = rInner
		}
		points[i] = [2]float64{cx + math.Cos(ang)*r, cy + math.Sin(ang)*r}
	}
	ebitenutil.DrawRect(screen, cx-rInner*0.8, cy-rInner*0.8, rInner*1.6, rInner*1.6, c)
	for i := 0; i < 10; i++ {
		j := (i + 1) % 10
		ebitenutil.DrawLine(screen, points[i][0], points[i][1], points[j][0], points[j][1], c)
	}
}

func drawCylinder(screen *ebiten.Image, x, y, w, h float64, c color.Color) {
	bodyTop := y + h*0.2
	bodyBottom := y + h*0.8
	ebitenutil.DrawRect(screen, x+2, bodyTop, w-4, bodyBottom-bodyTop, c)
	drawEllipseOutline(screen, x+w/2, bodyTop, w*0.48, h*0.2, color.Black)
	drawEllipseOutline(screen, x+w/2, bodyBottom, w*0.48, h*0.2, color.Black)
	ebitenutil.DrawLine(screen, x+2, bodyTop, x+2, bodyBottom, color.Black)
	ebitenutil.DrawLine(screen, x+w-2, bodyTop, x+w-2, bodyBottom, color.Black)
}

func drawEllipseOutline(screen *ebiten.Image, cx, cy, rx, ry float64, c color.Color) {
	segments := 24
	for i := 0; i < segments; i++ {
		a0 := 2 * math.Pi * float64(i) / float64(segments)
		a1 := 2 * math.Pi * float64(i+1) / float64(segments)
		x0 := cx + math.Cos(a0)*rx
		y0 := cy + math.Sin(a0)*ry
		x1 := cx + math.Cos(a1)*rx
		y1 := cy + math.Sin(a1)*ry
		ebitenutil.DrawLine(screen, x0, y0, x1, y1, c)
	}
}

func squareMask(size int) [][]bool {
	return rectangleMask(size, size)
}

func rectangleMask(width, height int) [][]bool {
	mask := make([][]bool, height)
	for y := range mask {
		mask[y] = make([]bool, width)
		for x := range mask[y] {
			mask[y][x] = true
		}
	}
	return mask
}

func starMask() [][]bool {
	return [][]bool{
		{false, false, true, false, false},
		{false, true, true, true, false},
		{true, true, true, true, true},
		{false, true, true, true, false},
		{true, false, true, false, true},
	}
}

func cylinderMask() [][]bool {
	return [][]bool{
		{false, true, true, true, false},
		{true, true, true, true, true},
		{true, true, true, true, true},
		{true, true, true, true, true},
		{false, true, true, true, false},
	}
}

func maskDimensions(mask [][]bool) (int, int) {
	if len(mask) == 0 {
		return 0, 0
	}
	return len(mask[0]), len(mask)
}
