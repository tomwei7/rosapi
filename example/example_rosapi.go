package main

import (
	"time"
	"fmt"
	"context"
	"log"

	"github.com/tomwei7/rosapi"
)

func main() {
	cc, err := rosapi.Dial("admin:password@192.168.88.1")
	if err != nil {
		log.Fatal(err)
	}
	defer cc.Close()

	go func() {
		streamOutput, err := cc.Stream(context.Background(), "/ip/dns/static/listen")
		if err != nil {
			log.Fatal(err)
		}
		for err == nil {
			var re rosapi.Re
			if re, err = streamOutput.Recv(); err == nil {
				log.Printf("Listen DNS: %v\n", re)
			}
		}
	}()

	go func() {
		for i := 0; i < 1024; i++ {
			name := fmt.Sprintf("name_%d", i)
			_, err := cc.Exec(context.Background(), "/ip/dns/static/add", "=name="+name, "=address=127.0.0.1")
			if err != nil {
				log.Fatal(err)
			}
			time.Sleep(time.Second)
		}
		
	}()

	ch := make(chan struct{})
	<-ch
}
