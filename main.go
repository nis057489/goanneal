package main

import (
	"log"
	"math/rand"
	"time"

	"github.com/hajimehoshi/ebiten/v2"
	"github.com/nick/goanneal/solver"
	"github.com/nick/goanneal/tile"
	"github.com/nick/goanneal/viz"
)

func main() {
	rand.Seed(time.Now().UnixNano())

	grid := tile.NewGrid(20, 20)

	// Add some constrained tiles (red)
	grid.AddTile(5, 5, tile.Red, true)
	grid.AddTile(15, 15, tile.Red, true)
	grid.AddTile(5, 15, tile.Red, true)
	grid.AddTile(15, 5, tile.Red, true)

	// Add random unconstrained tiles
	for i := 0; i < 100; i++ {
		x := rand.Intn(20)
		y := rand.Intn(20)
		color := tile.Color(rand.Intn(4))
		grid.AddTile(x, y, color, false)
	}

	s := solver.NewSolver(grid)
	game := viz.NewGame(s)

	ebiten.SetWindowSize(600, 600)
	ebiten.SetWindowTitle("Tile Annealing")

	if err := ebiten.RunGame(game); err != nil {
		log.Fatal(err)
	}
}
