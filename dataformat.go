package gocamel

import "fmt"

// DataFormat converts message bodies between their wire representation and a
// Go model representation.
type DataFormat interface {
	Marshal(exchange *Exchange, body any) (any, error)
	Unmarshal(exchange *Exchange, body any) (any, error)
}

// MarshalProcessor applies a DataFormat marshal operation to the exchange body.
type MarshalProcessor struct {
	DataFormat DataFormat
}

// Process implements Processor.
func (p *MarshalProcessor) Process(exchange *Exchange) error {
	if p == nil || p.DataFormat == nil {
		return fmt.Errorf("marshal dataformat is nil")
	}
	body, err := p.DataFormat.Marshal(exchange, exchange.GetIn().GetBody())
	if err != nil {
		return err
	}
	exchange.GetIn().SetBody(body)
	exchange.GetOut().SetBody(body)
	return nil
}

// UnmarshalProcessor applies a DataFormat unmarshal operation to the exchange body.
type UnmarshalProcessor struct {
	DataFormat DataFormat
}

// Process implements Processor.
func (p *UnmarshalProcessor) Process(exchange *Exchange) error {
	if p == nil || p.DataFormat == nil {
		return fmt.Errorf("unmarshal dataformat is nil")
	}
	body, err := p.DataFormat.Unmarshal(exchange, exchange.GetIn().GetBody())
	if err != nil {
		return err
	}
	exchange.GetIn().SetBody(body)
	exchange.GetOut().SetBody(body)
	return nil
}
