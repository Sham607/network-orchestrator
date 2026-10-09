// Package audit owns the append-only trail of operator actions, especially
// writes to the network. It is separate from operational logging.
//
// The sync worker records one entry per cycle through the Recorder interface.
package audit
