// Command demo is a minimal demonstration of the slidingcounter library.
package main

import (
	"fmt"
	"log"
	"time"

	"slidingcounter"
)

func main() {
	c, err := slidingcounter.New(time.Minute, 10*time.Second)
	if err != nil {
		log.Fatal(err)
	}

	must := func(err error) {
		if err != nil {
			log.Fatal(err)
		}
	}

	must(c.Add("requests", 1))
	must(c.Add("requests", 4))
	must(c.Add("errors", 2))

	fmt.Println("Count(requests):", c.Count("requests"))
	fmt.Println("Count(errors):  ", c.Count("errors"))
	fmt.Println("Len:            ", c.Len())

	fmt.Println("Reset(errors):  ", c.Reset("errors"))
	fmt.Println("Count(errors):  ", c.Count("errors"))

	fmt.Println("Snapshot:")
	for _, e := range c.Snapshot() {
		fmt.Printf("  %s = %d\n", e.Key, e.Value)
	}
}
