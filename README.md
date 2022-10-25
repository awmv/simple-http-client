# MVP: Simple HTTP Client

Quick solution to perform concurrent HTTP requests to an endpoint that is expected to return a high volume of internal server errors. For simplicity it keeps track of the process with a textfile.

The idea here was lower the initial number of HTTP calls that another service needs to perform to that endpoint.

Build

```
go build subscribe.go
```

Run

```
./subscribe 8 sourcefile.txt
```

- `8` is the number of workers for the WaitGroup
- `sourcefile.txt` is the file containing IMEIs seperated by \n

Dev

```
go run subscribe.go 8 sourcefile.txt
```
