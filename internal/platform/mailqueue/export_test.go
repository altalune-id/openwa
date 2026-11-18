package mailqueue

func WakerChan(w *Waker) <-chan struct{} { return w.ch }
