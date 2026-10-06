// Command demo is a minimal showcase of the slidingcounter library.
// It performs a few Adds, Counts, a Reset and a Snapshot using the
// system clock, without sleeping or any argument parsing.
package main

import (
	"fmt"
	"log"
	"time"

	"slidingcounter"
)

func main() {
	c, err := slidingcounter.NewCounter(10*time.Second, time.Second)
	if err != nil {
		log.Fatal(err)
	}

	add := func(key string, delta int64) {
		if err := c.Add(key, delta); err != nil {
			log.Fatal(err)
		}
	}

	add("alice", 1)
	add("alice", 2)
	add("bob", 5)
	add("carol", 3)

	fmt.Println("Count(alice):", c.Count("alice"))
	fmt.Println("Count(bob):  ", c.Count("bob"))
	fmt.Println("Len():       ", c.Len())
	fmt.Println("Snapshot():  ", c.Snapshot())

	fmt.Println("Reset(alice):", c.Reset("alice"))
	fmt.Println("Count(alice):", c.Count("alice"))
	fmt.Println("Snapshot():  ", c.Snapshot())
}
