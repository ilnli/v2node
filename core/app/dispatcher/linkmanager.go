package dispatcher

import (
	"net"
	sync "sync"

	"github.com/xtls/xray-core/common"
	"github.com/xtls/xray-core/common/buf"
)

type ManagedWriter struct {
	connection net.Conn
	writer     buf.Writer
	manager    *LinkManager
}

func (w *ManagedWriter) WriteMultiBuffer(mb buf.MultiBuffer) error {
	return w.writer.WriteMultiBuffer(mb)
}

func (w *ManagedWriter) Close() error {
	w.manager.RemoveWriter(w)
	return common.Close(w.writer)
}

type LinkManager struct {
	links  map[*ManagedWriter]buf.Reader
	mu     sync.RWMutex
	closed bool
}

func (m *LinkManager) AddLink(writer *ManagedWriter, reader buf.Reader) {
	m.mu.Lock()
	if !m.closed {
		m.links[writer] = reader
		m.mu.Unlock()
		return
	}
	m.mu.Unlock()
	// A stream may finish authentication while its node is shutting down.
	// Closing it here prevents it from escaping the final traffic snapshot.
	common.Close(writer.writer)
	common.Interrupt(reader)
	if writer.connection != nil {
		writer.connection.Close()
	}
}

func (m *LinkManager) RemoveWriter(writer *ManagedWriter) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if !m.closed {
		delete(m.links, writer)
	}
}

func (m *LinkManager) CloseAll() {
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return
	}
	m.closed = true

	links := m.links
	m.links = make(map[*ManagedWriter]buf.Reader)
	m.mu.Unlock()

	for w, r := range links {
		// Some protocol readers/writers are wrappers without Close/Interrupt.
		// Retaining the actual inbound connection ensures these streams stop too.
		if w.connection != nil {
			w.connection.Close()
		}
		common.Close(w.writer)
		common.Interrupt(r)
	}
}
