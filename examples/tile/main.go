package main

import (
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
	state         *TileState
	temp          float64
	coolRate      float64
	tileSize      int
	steps         int
	accepts       int
	improves      int
	bestCost      float64
	lastPrint     float64
	repairCounter int
	bestState     *TileState // Add best state tracking
}

func (g *Game) Update() error {
	if g.temp > 0.1 { // minimum temperature
		g.steps++
		g.repairCounter++
		currentEnergy := g.state.Energy()

		if g.steps == 1 {
			g.bestCost = currentEnergy
			g.bestState = g.state.Copy().(*TileState)
			fmt.Printf("\nStep\tTemp\t\tEnergy\t\tAccepts\tImproves\tBest\n")
		}

		// Store previous state before any changes
		prevState := g.state.Copy().(*TileState)

		// Handle repairs
		if g.repairCounter >= 1000 {
			g.repairCounter = 0
			g.state.RepairConstraints()
			newEnergy := g.state.Energy()

			// Always accept repair results, but track if it's the best
			if newEnergy < g.bestCost {
				g.bestCost = newEnergy
				g.bestState = g.state.Copy().(*TileState)
			}
			return nil
		}

		// Regular annealing move
		g.state.Move()
		newEnergy := g.state.Energy()
		delta := newEnergy - currentEnergy

		if newEnergy > 1000000 { // Hard constraint violation
			g.state = prevState
		} else if delta <= 0 {
			g.accepts++
			if newEnergy < g.bestCost {
				g.improves++
				g.bestCost = newEnergy
				g.bestState = g.state.Copy().(*TileState)
			}
		} else if math.Exp(-delta/g.temp) < rand.Float64() {
			g.state = prevState
		} else {
			g.accepts++
		}

		// Print progress every 100 steps
		if float64(g.steps)-g.lastPrint >= 100 {
			fmt.Printf("%d\t%.6f\t%.2f\t\t%.2f%%\t%.2f%%\t%.2f\n",
				g.steps,
				g.temp,
				newEnergy,
				100.0*float64(g.accepts)/float64(g.steps),
				100.0*float64(g.improves)/float64(g.steps),
				g.bestCost)
			g.lastPrint = float64(g.steps)
		}

		g.temp *= g.coolRate
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

	for _, t := range g.state.Grid.Tiles {
		x := float64(t.X * g.tileSize)
		y := float64(t.Y * g.tileSize)

		c := colors[t.Color]
		if t.Constrained {
			c = colors[0] // Red for constrained tiles
		}

		ebitenutil.DrawRect(screen, x, y, float64(g.tileSize-1), float64(g.tileSize-1), c)
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

	// Add some constrained tiles (red)
	grid.AddTile(5, 5, Red, true)
	grid.AddTile(15, 15, Red, true)
	grid.AddTile(5, 15, Red, true)
	grid.AddTile(15, 5, Red, true)

	// Add random unconstrained tiles
	for i := 0; i < 100; i++ {
		x := rand.Intn(20)
		y := rand.Intn(20)
		color := Color(rand.Intn(4))
		grid.AddTile(x, y, color, false)
	}

	state := &TileState{Grid: grid}

	game := &Game{
		state:         state,
		temp:          25.0,
		coolRate:      0.999, // slower cooling for visual feedback
		tileSize:      30,
		bestCost:      math.MaxFloat64,
		repairCounter: 0,
		bestState:     state.Copy().(*TileState),
	}

	ebiten.SetWindowSize(600, 600)
	ebiten.SetWindowTitle("Tile Annealing")

	if err := ebiten.RunGame(game); err != nil {
		log.Fatal(err)
	}
}
