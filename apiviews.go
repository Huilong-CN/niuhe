package niuhe

import (
	"errors"
	"math"
	"net/http"
	"reflect"
	"regexp"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/ziipin-server/zpform"
)

const (
	GET        int  = 1
	POST       int  = 2
	GET_POST   int  = 3
	abortIndex int8 = math.MaxInt8 / 2
)

type HandlerFunc func(*Context)

type routeInfo struct {
	Methods    int
	Path       string
	handleFunc gin.HandlerFunc
}

type Module struct {
	urlPrefix   string
	middlewares []HandlerFunc
	handlers    []routeInfo
}

func NewModule(urlPrefix string) *Module {
	return &Module{
		urlPrefix:   urlPrefix,
		middlewares: make([]HandlerFunc, 0),
		handlers:    make([]routeInfo, 0),
	}
}

func (mod *Module) Use(middlewares ...HandlerFunc) *Module {
	mod.middlewares = append(mod.middlewares, middlewares...)
	return mod
}

type Context struct {
	gin.Context
	index    int8
	handlers []HandlerFunc
}

func newContext(c *gin.Context, middlewares []HandlerFunc) *Context {
	return &Context{Context: *c, index: -1, handlers: middlewares}
}

func (c *Context) Next() {
	c.index++
	s := int8(len(c.handlers))
	for ; c.index < s; c.index++ {
		c.handlers[c.index](c)
	}
}

func parseName(camelName string) string {
	re := regexp.MustCompile("[A-Z][a-z0-9]*")
	parts := re.FindAllString(camelName, -1)
	return strings.Join(parts, "_")
}

type IApiProtocol interface {
	Read(*http.Request, reflect.Value) error
	Write(*Context, reflect.Value, error) error
}

type DefaultApiProtocol struct{}

func (self DefaultApiProtocol) Read(request *http.Request, reqValue reflect.Value) error {
	return zpform.ReadReflectedStructForm(request, reqValue)
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
	return defaultApiProtocol
}

func (mod *Module) Register(group interface{}, middlewares ...HandlerFunc) *Module {
	return mod.RegisterWithProtocolFactory(group, ApiProtocolFactoryFunc(GetDefaultApiProtocol), middlewares...)
}

func (mod *Module) RegisterWithProtocolFactoryFunc(group interface{}, pff func() IApiProtocol, middlewares ...HandlerFunc) *Module {
	return mod.RegisterWithProtocolFactory(group, ApiProtocolFactoryFunc(pff), middlewares...)
}

func (mod *Module) RegisterWithProtocolFactory(group interface{}, pf IApiProtocolFactory, middlewares ...HandlerFunc) *Module {
	groupType := reflect.TypeOf(group)
	groupName := groupType.Elem().Name()
	for i := 0; i < groupType.NumMethod(); i++ {
		m := groupType.Method(i)
		name := m.Name
		var methods int
		if strings.HasSuffix(name, "_GET") {
			methods = GET
			name = name[:len(name)-len("_GET")]
		} else if strings.HasSuffix(name, "_POST") {
			methods = POST
			name = name[:len(name)-len("_POST")]
		} else {
			methods = GET_POST
		}
		path := strings.ToLower("/" + parseName(groupName) + "/" + parseName(name) + "/")
		mod._Register(methods, path, m.Func, pf, middlewares)
	}
	return mod
}

var bindFunc reflect.Value

func getApiGinFunc(nilGroupValue, funcValue reflect.Value, reqType, rspType reflect.Type, pf IApiProtocolFactory, middlewares []HandlerFunc) func(*gin.Context) {
	return func(c *gin.Context) {
		req := reflect.New(reqType)
		rsp := reflect.New(rspType)
		var ierr interface{}
		protocol := pf.GetProtocol()
		if formErr := protocol.Read(c.Request, req); formErr != nil {
			ierr = formErr
		}
		context := newContext(c, middlewares)
		if ierr == nil {
			context.handlers = append(context.handlers, func(c *Context) {
				outs := funcValue.Call([]reflect.Value{
					nilGroupValue,
					reflect.ValueOf(context),
					req,
					rsp,
				})
				ierr = outs[0].Interface()
			})
			context.Next()
		}
		var rspErr error
		if ierr != nil {
			if err, ok := ierr.(error); ok {
				rspErr = err
			} else {
				rspErr = errors.New("unknown error")
			}
		} else {
			rspErr = nil
		}
		if err := protocol.Write(context, rsp, rspErr); err != nil {
			panic(err)
		}
	}
}

func getWebGinFunc(nilGroupValue, funcValue reflect.Value, middlewares []HandlerFunc) func(*gin.Context) {
	return func(c *gin.Context) {
		context := newContext(c, middlewares)
		context.handlers = append(context.handlers, func(c *Context) {
			funcValue.Call([]reflect.Value{
				nilGroupValue,
				reflect.ValueOf(context),
			})
		})
		context.Next()
	}
}

func (mod *Module) _Register(methods int, path string, funcValue reflect.Value, pf IApiProtocolFactory, middlewares []HandlerFunc) *Module {
	funcType := funcValue.Type()
	if funcType.Kind() != reflect.Func {
		panic("handleFunc必须为函数")
	}
	var isApi bool
	if funcType.NumIn() == 4 && funcType.NumOut() == 1 {
		isApi = true
	} else if funcType.NumIn() == 2 && funcType.NumOut() == 0 {
		isApi = false
	} else {
		panic("handleFunc必须有三个参数,并且只返回一个error")
	}
	groupType := funcType.In(0)
	nilGroupValue := reflect.Zero(groupType)
	middlewares = append(mod.middlewares, middlewares...)
	if isApi {
		reqType := funcType.In(2).Elem()
		rspType := funcType.In(3).Elem()
		ginHandler := getApiGinFunc(nilGroupValue, funcValue, reqType, rspType, pf, middlewares)
		mod.handlers = append(mod.handlers, routeInfo{Methods: methods, Path: path, handleFunc: ginHandler})
	} else {
		ginHandler := getWebGinFunc(nilGroupValue, funcValue, middlewares)
		mod.handlers = append(mod.handlers, routeInfo{Methods: methods, Path: path, handleFunc: ginHandler})
	}
	return mod
}

func init() {
	cType := reflect.TypeOf(&gin.Context{})
	bindMethod, found := cType.MethodByName("Bind")
	if !found {
		panic("Cannot find Bind mathod")
	}
	bindFunc = bindMethod.Func
	defaultApiProtocol = &DefaultApiProtocol{}
}
