package niuhe

import (
	"net/http"
	"reflect"

	"github.com/ziipin-server/zpform"
)

type IApiProtocol interface {
	Read(*http.Request, reflect.Value) error
	Write(*Context, reflect.Value, error) error
}

type DefaultApiProtocol struct{}

func (self DefaultApiProtocol) Read(request *http.Request, reqValue reflect.Value) error {
	if err := zpform.ReadReflectedStructForm(request, reqValue); err != nil {
		return NewCommError(-1, err.Error())
	}
	return nil
}

func (self DefaultApiProtocol) Write(c *Context, rsp reflect.Value, err error) error {
	var response map[string]interface{}
	if err != nil {
		if commErr, ok := err.(ICommError); ok {
			response = map[string]interface{}{
				"result":  commErr.GetCode(),
				"message": commErr.GetMessage(),
			}
		} else {
			response = map[string]interface{}{
				"result":  -1,
				"message": err.Error(),
			}
		}
	} else {
		response = map[string]interface{}{
			"result": 0,
			"data":   rsp.Interface(),
		}
	}
	c.JSON(200, response)
	return nil
}

type IApiProtocolFactory interface {
	GetProtocol() IApiProtocol
}

type ApiProtocolFactoryFunc func() IApiProtocol

func (f ApiProtocolFactoryFunc) GetProtocol() IApiProtocol {
	return f()
}

var defaultApiProtocol *DefaultApiProtocol

func GetDefaultApiProtocol() IApiProtocol {
	LogDebug("GetDefaultApiProtocol")
	return defaultApiProtocol
}

var defaultApiProtocolFactory IApiProtocolFactory

func GetDefaultProtocolFactory() IApiProtocolFactory {
	return defaultApiProtocolFactory
}

func SetDefaultProtocolFactory(pf IApiProtocolFactory) {
	defaultApiProtocolFactory = pf
}

func init() {
	defaultApiProtocol = &DefaultApiProtocol{}
	defaultApiProtocolFactory = ApiProtocolFactoryFunc(GetDefaultApiProtocol)
}
