package niuhe

import (
	"errors"
	"math"
	"reflect"
	"regexp"
	"strings"

	"github.com/gin-gonic/gin"
)

const (
	GET        int  = 1
	POST       int  = 2
	GET_POST   int  = 3
	abortIndex int8 = math.MaxInt8 / 2
)

type HandlerFunc func(*Context)

type routeInfo struct {
	Methods     int
	Path        string
	HandleFunc  gin.HandlerFunc
	groupValue  reflect.Value
	funcValue   reflect.Value
	pf          IApiProtocolFactory
	middlewares []HandlerFunc
}

type Module struct {
	urlPrefix   string
	middlewares []HandlerFunc
	routers     []*routeInfo
}

func NewModule(urlPrefix string) *Module {
	return &Module{
		urlPrefix:   urlPrefix,
		middlewares: make([]HandlerFunc, 0),
		routers:     make([]*routeInfo, 0),
	}
}

func (mod *Module) Use(middlewares ...HandlerFunc) *Module {
	mod.middlewares = append(mod.middlewares, middlewares...)
	return mod
}

func parseName(camelName string) string {
	re := regexp.MustCompile("[A-Z][a-z0-9]*")
	parts := re.FindAllString(camelName, -1)
	return strings.Join(parts, "_")
}

func (mod *Module) Register(group interface{}, middlewares ...HandlerFunc) *Module {
	return mod.RegisterWithProtocolFactory(group, nil, middlewares...)
}

func (mod *Module) RegisterWithProtocolFactoryFunc(group interface{}, pff func() IApiProtocol, middlewares ...HandlerFunc) *Module {
	return mod.RegisterWithProtocolFactory(group, ApiProtocolFactoryFunc(pff), middlewares...)
}

type IModuleGroup interface {
	Register(*Module, IApiProtocolFactory, ...HandlerFunc)
}

func (mod *Module) RegisterWithProtocolFactory(group interface{}, pf IApiProtocolFactory, middlewares ...HandlerFunc) *Module {
	if modGroup, ok := group.(IModuleGroup); ok {
		modGroup.Register(mod, pf, middlewares...)
	} else {
		groupType := reflect.TypeOf(group)
		groupName := groupType.Elem().Name()
		groupValue := reflect.ValueOf(group)
		for i := 0; i < groupType.NumMethod(); i++ {
			m := groupType.Method(i)
			name := m.Name
			firstCh := name[:1]
			if firstCh < "A" || firstCh > "Z" { // Skip private method(s)
				continue
			}
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
			mod.routers = append(mod.routers, &routeInfo{
				Methods:     methods,
				Path:        path,
				groupValue:  groupValue,
				funcValue:   m.Func,
				pf:          pf,
				middlewares: middlewares,
			})
		}
	}
	return mod
}

func getApiGinFunc(groupValue, funcValue reflect.Value, reqType, rspType reflect.Type, pf IApiProtocolFactory, middlewares []HandlerFunc) gin.HandlerFunc {
	return func(c *gin.Context) {
		req := reflect.New(reqType)
		rsp := reflect.New(rspType)
		var ierr interface{}
		var protocol IApiProtocol
		if pf == nil {
			protocol = GetDefaultProtocolFactory().GetProtocol()
		} else {
			protocol = pf.GetProtocol()
		}
		if formErr := protocol.Read(c.Request, req); formErr != nil {
			ierr = formErr
		}
		context := newContext(c, middlewares)
		if ierr == nil {
			context.handlers = append(context.handlers, func(c *Context) {
				outs := funcValue.Call([]reflect.Value{
					groupValue,
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

func getWebGinFunc(groupValue, funcValue reflect.Value, middlewares []HandlerFunc) gin.HandlerFunc {
	return func(c *gin.Context) {
		context := newContext(c, middlewares)
		context.handlers = append(context.handlers, func(c *Context) {
			funcValue.Call([]reflect.Value{
				groupValue,
				reflect.ValueOf(context),
			})
		})
		context.Next()
	}
}

func getGinFunc(
	groupValue reflect.Value, methods int, path string, funcValue reflect.Value, pf IApiProtocolFactory, middlewares []HandlerFunc,
) (ginHandler gin.HandlerFunc) {
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
		panic("handleFunc必须有一个（*niuhe.Context)或三个(*niuhe.Context, *ReqMsg, *RspMsg)参数,并且只返回一个error")
	}
	if isApi {
		reqType := funcType.In(2).Elem()
		rspType := funcType.In(3).Elem()
		ginHandler = getApiGinFunc(groupValue, funcValue, reqType, rspType, pf, middlewares)
	} else {
		ginHandler = getWebGinFunc(groupValue, funcValue, middlewares)
	}
	return
}

func (mod *Module) Routers(svrMiddlewares []HandlerFunc) []*routeInfo {
	for _, router := range mod.routers {
		middlewares := append(mod.middlewares, svrMiddlewares...)
		middlewares = append(middlewares, router.middlewares...)
		router.HandleFunc = getGinFunc(
			router.groupValue, router.Methods, router.Path, router.funcValue, router.pf, middlewares)
	}
	return mod.routers
}
