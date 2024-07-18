package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/go-redis/redis/v8"
	"github.com/gorilla/websocket"
)

var client *redis.Client
var Users map[string]*websocket.Conn
var sub *redis.PubSub

const chatChannel = "chats"

var upgrader = websocket.Upgrader{}

func init() {
	Users = map[string]*websocket.Conn{}
}

func chat(w http.ResponseWriter, r *http.Request) {
	user := strings.TrimPrefix(r.URL.Path, "/chat/")

	upgrader.CheckOrigin = func(r *http.Request) bool {
		return true
	}
	c, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Print("upgrade:", err)
		return
	}

	Users[user] = c
	fmt.Println(user, "in chat")

	for {
		_, message, err := c.ReadMessage()
		if err != nil {
			_, ok := err.(*websocket.CloseError)
			if ok {
				fmt.Println("connection closed by", user)
				err := c.Close()
				if err != nil {
					fmt.Println("error closing ws connection", err)
				}
				delete(Users, user)
				fmt.Println("closed websocket connection and remove user")
			}
			break
		}
		client.Publish(context.Background(), chatChannel, user+":"+string(message)).Err()
		if err != nil {
			fmt.Println("Publish failed", err)
		}
	}

}

func startChatBroadcaster() {
	go func() {
		fmt.Println("listening to messages")
		sub = client.Subscribe(context.Background(), chatChannel)
		messages := sub.Channel()
		for message := range messages {
			from := strings.Split(message.Payload, ":")[0]
			//broadcast to all
			for user, peer := range Users {
				if from != user {
					peer.WriteMessage(websocket.TextMessage, []byte(message.Payload))
				}
			}
		}
	}()
}

func main() {
	client = redis.NewClient(&redis.Options{Addr: "localhost:6379"})
	_, err := client.Ping(context.Background()).Result()

	if err != nil {
		log.Fatal("ping failed. could not connect", err)
	}

	startChatBroadcaster()

	http.HandleFunc("/chat/", chat)

	server := http.Server{Addr: ":8080", Handler: nil}

	go func() {
		fmt.Println("started server")
		err := server.ListenAndServe()
		if err != nil && err != http.ErrServerClosed {
			log.Fatal("failed to start server", err)
		}
	}()

	exit := make(chan os.Signal, 1)
	signal.Notify(exit, syscall.SIGTERM, syscall.SIGINT)
	<-exit

	fmt.Println("exit signaled")

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	for _, conn := range Users {
		conn.Close()
	}

	sub.Unsubscribe(context.Background(), chatChannel)
	sub.Close()

	server.Shutdown(ctx)

	fmt.Println("app shutdown")
}
