package main
import (
	"fmt"
	"net/http"
	"io"
)

type pair struct{
	x int
	y error
}

func server(){
	go func(){
		err := http.ListenAndServe(":8080",pair{})
		fmt.Println(err)
	}()
	requestServer()
}

func (p pair) ServeHTTP(w http.ResponseWriter, r *http.Request){
	w.Write()([]byte("serve"))
} 

func requestServer(){
	resp, err := http.Get("http://localhost:8080")
	fmt.Println(err)
	defer resp.Body.Close()
	body,err := io.ReadAll(resp.Body)
	fmt.Println("\nWebserver said: `%s`", string(body))
}



func main(){
	
}