package main

import (
	"container/heap"
	"fmt"
	"image/color"
	"log"
	"math"
	"math/rand"
	"time"

	"github.com/hajimehoshi/ebiten/v2"
	"github.com/hajimehoshi/ebiten/v2/ebitenutil"
)

type Game struct {
	state          *TileState
	temp           float64
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
}

func (g *Game) Update() error {
	if g.temp > 0.1 && !g.state.Done {
		// Do multiple moves per update
		for i := 0; i < g.movesPerUpdate; i++ {
			g.steps++
			g.repairCounter++
			currentEnergy := g.state.Energy()

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

			// Store state only if needed
			var prevState *TileState
			prevEnergy := currentEnergy

			// Regular annealing move
			g.state.Move()
			newEnergy := g.state.Energy()
			delta := newEnergy - prevEnergy

			if newEnergy > 1000000 || // Hard constraint violation
				(delta > 0 && math.Exp(-delta/g.temp) < rand.Float64()) {
				if prevState == nil {
					prevState = g.state.Copy().(*TileState)
				}
				g.state = prevState
			} else {
				g.accepts++
				if newEnergy < g.bestCost {
					g.improves++
					g.bestCost = newEnergy
					g.bestState = g.state.Copy().(*TileState)
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
	} else if g.state.Done && g.steps > 0 {
		// Print best positions once when done
		g.state.PrintBestPositions(3)
		g.steps = 0 // Prevent printing again
	}
	return nil
}

func (g *Game) Draw(screen *ebiten.Image) {
	colors := []color.RGBA{
		{255, 0, 0, 255},   // Red
		{0, 0, 255, 255},   // Blue
		{0, 255, 0, 255},   // Green
		{255, 255, 0, 255}, // Yellow
	}

	// Draw existing tiles
	for _, t := range g.state.Grid.Tiles {
		x := float64(t.X * g.tileSize)
		y := float64(t.Y * g.tileSize)

		c := colors[t.Color]
		if t.Constrained {
			c = colors[0] // Red for constrained tiles
		}

		ebitenutil.DrawRect(screen, x, y, float64(g.tileSize-1), float64(g.tileSize-1), c)
	}

	// Draw polygon tiles if it exists
	if g.state.Polygon != nil {
		polyColor := color.RGBA{128, 0, 128, 255} // Purple for polygon
		for _, t := range g.state.Polygon.GetWorldTiles() {
			x := float64(t.X * g.tileSize)
			y := float64(t.Y * g.tileSize)
			ebitenutil.DrawRect(screen, x, y, float64(g.tileSize-1), float64(g.tileSize-1), polyColor)
		}
	}

	if g.state.Done {
		msg := "No valid configuration found!"
		if g.state.Energy() == 0 {
			msg = "Solution found!"
		}
		ebitenutil.DebugPrint(screen, msg)
	}
}

func (g *Game) Layout(w, h int) (int, int) {
	return g.state.Grid.Width * g.tileSize, g.state.Grid.Height * g.tileSize
}

func main() {
	rand.Seed(time.Now().UnixNano())

	grid := &Grid{
		Width:  20,
		Height: 20,
		Tiles:  make([]*Tile, 0),
	}

	// Create a set to track constrained positions
	constrained := make(map[string]bool)

	// Add constrained tiles first
	for x := 3; x < 17; x += 5 {
		for y := 3; y < 17; y += 5 {
			grid.AddTile(x, y, Red, true)
			constrained[fmt.Sprintf("%d,%d", x, y)] = true
		}
	}

	// Add border constraints
	for x := 0; x < 20; x += 5 {
		grid.AddTile(x, 0, Red, true)
		grid.AddTile(x, 19, Red, true)
		constrained[fmt.Sprintf("%d,%d", x, 0)] = true
		constrained[fmt.Sprintf("%d,%d", x, 19)] = true
	}
	for y := 0; y < 20; y += 5 {
		grid.AddTile(0, y, Red, true)
		grid.AddTile(19, y, Red, true)
		constrained[fmt.Sprintf("%d,%d", 0, y)] = true
		constrained[fmt.Sprintf("%d,%d", 19, y)] = true
	}

	// Add random unconstrained tiles, avoiding constrained positions
	for i := 0; i < 150; i++ {
		x := rand.Intn(20)
		y := rand.Intn(20)
		if !constrained[fmt.Sprintf("%d,%d", x, y)] {
			color := Color(1 + rand.Intn(3)) // Avoid red
			grid.AddTile(x, y, color, false)
		}
	}

	// Create a larger polygon to place and ensure valid starting position
	polygon := NewRectangle(5, 4, Blue)
	polygon.PosX = 4
	polygon.PosY = 4

	state := &TileState{
		Grid:           grid,
		Polygon:        polygon,
		validPositions: make(ValidPositionQueue, 0),
	}
	heap.Init(&state.validPositions)

	// Validate initial position
	if !state.isValidPosition() {
		// Try to find a valid starting position
		found := false
		for x := 0; x < grid.Width-4; x++ {
			for y := 0; y < grid.Height-4; y++ {
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
			log.Fatal("Could not find valid starting position for polygon")
		}
	}

	game := &Game{
		state:          state,
		temp:           25.0,
		coolRate:       0.999, // slower cooling for visual feedback
		tileSize:       30,
		bestCost:       math.MaxFloat64,
		repairCounter:  0,
		bestState:      state.Copy().(*TileState),
		movesPerUpdate: 10, // Perform 10 moves per visual update
	}

	ebiten.SetWindowSize(600, 600)
	ebiten.SetWindowTitle("Tile Annealing")

	if err := ebiten.RunGame(game); err != nil {
		log.Fatal(err)
	}
}
