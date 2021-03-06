package firego

import (
	"bufio"
	"encoding/json"
	"log"
	"strings"
)

// Event represents a notification received when watching a
// firebase reference
type Event struct {
	// Type of event that was received
	Type string
	// Path to the data that changed
	Path string
	// Data that changed
	Data interface{}
}

// StopWatching stops tears down all connections that are watching
func (fb *Firebase) StopWatching() {
	if fb.watching {
		// signal connection to terminal
		fb.stopWatching <- struct{}{}
		// flip the bit back to not watching
		fb.watching = false
	}
}

// Watch listens for changes on a firebase instance and
// passes over to the given chan.
//
// Only one connection can be established at a time. The
// second call to this function without a call to fb.StopWatching
// will close the channel given and return nil immediately
func (fb *Firebase) Watch(notifications chan Event) error {
	if fb.watching {
		close(notifications)
		return nil
	}

	// build SSE request
	req, err := fb.makeRequest("GET", nil)
	if err != nil {
		return err
	}
	req.Header.Add("Accept", "text/event-stream")

	// do request
	resp, err := fb.client.Do(req)
	if err != nil {
		return err
	}

	// set watching flag
	fb.watching = true

	// start parsing response body
	go func() {
		// build scanner for response body
		scanner := bufio.NewScanner(resp.Body)
		// set custom split function for SSE events
		scanner.Split(eventSplit)

		// monitor the stopWatching channel
		// if we're told to stop, close the response Body
		go func() {
			<-fb.stopWatching
			resp.Body.Close()
		}()
	scanning:
		for scanner.Scan() {
			// split event string
			// 		event: put
			// 		data: {"path":"/","data":{"foo":"bar"}}
			parts := strings.Split(scanner.Text(), "\n")

			// create a base event
			event := Event{
				Type: strings.Replace(parts[0], "event: ", "", 1),
			}

			// should be reacting differently based off the type of event
			switch event.Type {
			case "put", "patch": // we've got extra data we've got to parse

				// the extra data is in json format
				var data map[string]interface{}
				if err := json.Unmarshal([]byte(strings.Replace(parts[1], "data: ", "", 1)), &data); err != nil {
					log.Fatal(err)
				}

				// set the extra fields
				event.Path = data["path"].(string)
				event.Data = data["data"]

				// ship it
				notifications <- event
			case "keep-alive":
				// received ping - nothing to do here
			case "cancel":
				// The data for this event is null
				// This event will be sent if the Security and Firebase Rules
				// cause a read at the requested location to no longer be allowed

				// send the cancel event
				notifications <- event
				break scanning
			case "auth_revoked":
				// The data for this event is a string indicating that a the credential has expired
				// This event will be sent when the supplied auth parameter is no longer valid

				// TODO: handle
			}
		}

		// call stop watching to reset state and cleanup routines
		fb.StopWatching()
		close(notifications)

		if err := scanner.Err(); err != nil {
			log.Printf("Error: %s\n", err)
		}
	}()
	return nil
}

func eventSplit(data []byte, atEOF bool) (int, []byte, error) {
	var (
		token   []byte
		advance int
		found   bool
	)

	for _, b := range data {
		token = append(token, b)
		advance++

		if b == '\n' {
			if found {
				break
			}
			found = true
		} else {
			found = false
		}
	}
	return advance, token, nil
}
