# Events of Necessity

**Evon** is a lightweight event dispatcher code generator for Golang. It reads special annotations from the comments on handler type definitions in the source code and generates fast and versatile event dispatcher code for you. It functions like .NET events / Qt signal-slots / Java listeners / Node.js ".on" s, yet provides more customizations.

[![Build Status](https://travis-ci.com/lych77/evon.svg?branch=master)](https://travis-ci.com/lych77/evon)

- [Quick Start](#quick-start)
- [Using Interfaces](#using-interfaces)
- [Annotations Detailed](#annotations-detailed)
- [Unsubscribing](#unsubscribing)
- [Thread Safety](#thread-safety)
- [Temporarily Disable Dispatching](#temporarily-disable-dispatching)
- [Parallelism](#parallelism)
- [Panic Handling](#panic-handling)
- [Dispatcher Chaining](#dispatcher-chaining)
- [Requirements on Handler Types](#requirements-on-handler-types)
- [Command Line Arguments](#command-line-arguments)
- [FAQ](#faq)

## Quick Start

Installation:

```shell
go get -u github.com/lych77/evon/cmd/evon
```


Define a function type in any source file in your package as the event handler, and annotate it with `@evon()` in its documenting comment：

```go
// @evon()
type LoginHandler func(uid int, addr string)
```

**Note** the `Handler` suffix of the type name is *required*. Then run the command under your package directory:

```bash
evon
```

A source file called `evon_gen.go` will be generated. Feel free to check what's in it, but to be quick, it's summarized as below:

```go
type LoginEvent struct { ... }   // Always has "Event" suffix

func NewLoginEvent() *LoginEvent { ... }
func (ev *LoginEvent) Sub(handler LoginHandler) { ... }
func (ev *LoginEvent) Emit(uid int, addr string) { ... }
func (ev *LoginEvent) Count() int { ... }
```

Above are the basics of an event dispatcher. To use it:

```go
// Instantiate a dispatcher
evt := NewLoginEvent()

// A handler func
func onLogin(uid int, addr string) {
    fmt.Printf("User %d logged in from %s\n", uid, addr)
}

// Another handler func
func onLogin1(uid int, addr string) {
    broadcast("User %d entered the room")
}

// Subscribe handlers to the dispatcher
evt.Sub(onLogin)
evt.Sub(onLogin1)

// Emit an event, every subscriber will get invoked
evt.Emit(123, "localhost")

// Get the current number of subscribers
fmt.Println("%d subscribers", evt.Count())
```

## Using Interfaces

Interfaces are supported as well:

```go
// @evon()
type SessionHandler interface {
    Login(uid int, addr string)
    Logout(uid int)
    Message(uid int, msg string)
}
```

For interface event dispatchers, `.Emit` is no longer a function but an *object* that implements the interface, and each method in this object is actually an emitter. When one is called, the corresponding method in each subscriber will get invoked.

```go
evt := NewSessionEvent()

// Handlers are implementors of this interface
type Session struct{}
func (ssn *Session) Login(uid int, addr string) { ... }
func (ssn *Session) Logout(uid int) { ... }
func (ssn *Session) Message(uid int, msg string) { ... }

evt.Sub(&Session{})   

// Emit different kinds of events
evt.Emit.Login(123, "localhost")
evt.Emit.Message(123, "What's up?")
evt.Emit.Logout(123)
```

## Annotations Detailed

All evon annotations have the `@evon(...)` form. Between the parentheses you can specify flags to customize the dispatcher implementation. All flags are predefined words, including: `catch`, `lock`, `pause`, `queue`, `spawn`, `unsub`, `wait`.

Multiple flags are separated by commas ( `,` ). For example:

- `@evon(unsub, lock)`
- `@evon(lock, wait, queue)`

Annotations are case-sensitive. The order of the flags doesn't matter. Some flags can only be used under certain conditions, will be detailed later.

`@evon` annotations apply only to `func` type or `interface` type definitions. They can reside *anywhere* within the documenting comment texts of the types, while there can be at most *one* annotation per type. For type groups, one annotation can be applied to affect all members in a group:

```go
// An example @evon(spawn, pause) for type groups
type (
    fooHandler func(a int, b string)
    barHandler func(c bool)
    intfHandler interface { ... }
    ...
)
```

While certain member in the group can also have its own annotation to completely override the group-level one.

A restriction to the names of annotated types exists: They must ends with a given suffix ( and cannot be the mere suffix ). Examples above used the default value `Handler`, which is recommended. It can be changed via the command line flag `-handler_suffix` of the `evon` command.

## Unsubscribing

For simplicity, by default a subscriber cannot unsubscribe from a dispatcher, and that's sufficient in many cases. However, if unsubscribing is really needed, use the `unsub` flag:

```go
// @evon(unsub)
type LoginHandler func(uid int, addr string)
```

This will change the form of the generated `Sub` method:

```go
func (ev *LoginEvent) Sub(handler LoginHandler) func() { ... }
```

Call the returned `func()` to unsubscribe from the dispatcher:

```go
unsub := evt.Sub(OnLogin)
unsub()
```

If the subscriber has already unsubscribed from the dispatcher, calling this function just does nothing and harms nothing.

Additionally, a `.Clear` method will be generated on the dispatcher type:

```go
func (ev *LoginEvent) Clear() { ... }
```

Which unsubscribes all existing subscribers from the dispatcher.

## Thread Safety

By default dispatchers are *not* thread-safe for performance, and this is satisfactory in many circumstances. In cases really requiring thread safety, the `lock` flag can be used, which adds a `sync.RWMutex` to the generated code to guard the subscriber list, keeping concurrent sub/unsub/emit operations from different goroutines out of race conditions:

```go
// @evon(lock)
type LoginHandler func(uid int, addr string)
```

If the subscriber list does not change any more after some initialization steps are finished, concurrent emit operations are always safe even without a lock. In such cases the `lock` flag is not necessary.

## Temporarily Disable Dispatching

```go
// @evon(pause)
type LoginHandler func(uid int, addr string)
```

The `pause` flag adds three more methods to the dispatcher:

```go
func (ev *LoginEvent) Pause() { ... }
func (ev *LoginEvent) Resume() { ... }
func (ev *LoginEvent) Paused() bool { ... }
```

A paused dispatcher will silently discard all emissions and never invoke any subscriber. All dispatchers are created *unpaused*, `Pause()` pauses a dispatcher and `Resume()` puts it back to normal. `Paused()` checks if it is currently paused.

## Parallelism

A dispatcher is by default a "synchronous" one, meaning the subscribers are invoked within the same goroutine who's calling the emitter, which can only return after all handler functions are executed one by one. This is the simplest case, and there are two flags that change the implementation:

- `@evon(spawn)`: Each invocation to any handler function is in a newly spawned goroutine, thus the emitter itself returns immediately, not waiting for any of the handlers to finish.

- `@evon(queue)`: All invocations to one subscriber ( regardless of which method ) are performed by the same goroutine, thus sequentialized. The emitter returns immediately too, not waiting for anybody. Invocations to different subscribers still run in parallel.
    - In this mode, The factory function of the dispatcher accepts one parameter: `qsize int`, to specify the length of the underlying `chan` that implements the queue. If the handler consumes queued events too slowly and finally made the queue full, subsequent emitter calls will block until rooms are made to store new events.

Only one of these two flags can be used in a time. Plus the default case, there are totally three kinds of implementations of dispatchers.

Sometimes it's still necessary to wait for all subscribers to finish, that's what the `wait` flag is for. This flag can only be used together with `spawn` or `queue`：

```go
// @evon(queue, wait)
type LoginHandler func(uid int, addr string)
```

Which changes the behavior of emitters to waiting for all subscribers to finish before returning. This is achieved by a `sync.WaitGroup`, and the subscribers still run in parallel.

## Panic Handling

By default evon leaves the chance of panic handling to the user, i.e. users should handle possible panics within handler functions by themselves. If they failed to do that, "synchronous" dispatchers will propagate the panic up to where the emitter is called, while `spawn` and `queue` dispatchers will just crash the whole process.

When evon is expected to handle such panics instead of user themselves, the `catch` flag can be used:

```go
// @evon(catch)
type LoginHandler func(uid int, addr string)

evt := NewLoginHandler(func(e interface{}){
    fmt.Printf("Error occurred: %s\n", e)
})
```

As shown, this flag adds a parameter to the factory function, which is a function takes an `interface{}` argument ( when there's already a `qsize`, this is following it ). When an unhandled panic occurs, this function is called with the panic value and everything else in the program is unaffected.

The panic handler will always be called by the same goroutine that has run the panicking event handler, and panics even within the panic handler are never handled again.

## Dispatcher Chaining

Dispatchers of the same type can be chained like the following:

```go
parent := NewSomeEvent()
child := NewSomeEvent()
child2 := NewSomeEvent()
grandChild := NewSomeEvent()

// Upstream emissions will be propagated to all downstream dispatchers
// Beware not to make loops
parent.Sub(child.Emit)
parent.Sub(child2.Emit)
child.Sub(grandChild.Emit)

parent.Emit(...)
// or 
parent.Emit.Foo(...)
```

`.Emit` itself is intended to be a valid handler of the event, regardless of being a function or an object.

Exception: for interface handlers, if the interface is not implementable within the current package ( i.e. any of its embedded interfaces that defined outside current package has unexported methods ), `.Emit` will just implement the available part of it, though not be implementing the whole interface. Such dispatchers cannot be chained.

## Requirements on Handler Types

There's almost no limitations on handler types besides the name suffix rule. But to cover the details, let's make clear of some points.

**Emitter parameters:**

- Ellipsis ( `...` ) parameters are supported.
- Parameter name omitting is supported ( dummy names will be created to make the generated code work ).
- Blank identifiers ( `_` ) are supported and are dealt with the same way as omitted ones.

**Emitter return values:**

- Return values are not meaningful and not recommended, though supported for compatibility, keep using returnless functions when possible.
- Handler return values are all discarded and would never be passed back to the emitter, while emitters always return meaningless "zero"s.
    - In some cases the emitter even returns before the results of the handlers come out.

**Interface embedding:**

- Embedding is supported, all methods at any embedding level will be detected and emitters for them are generated.
- Only the outermost interface type need to be annotated and to comply with the name suffix rule.
- The final interface must have at least one method, as subscribers that can't receive events are meaningless.

**Type redefinitions:**

- Both `type A B` and `type A = B` are supported and behave the same, as long as the bottommost type satisfies the requirements.
- Only the topmost type need to be annotated and to comply with the name suffix rule.
    - Even if multiple types in the same definition chain are annotated, they are different event handler types despite similar shapes.

**Foreign types:**

- Any underlying or component type of the handler type, at any recursion level, can be from other packages.

**Exported/unexported names:**

- The generated names of the dispatcher type and its factory function will be the same kind as the provided handler type's.

## Command Line Arguments

```
-event_suffix string
    Suffix of the generated event type names (default "Event")
-handler_suffix string
    Required suffix of the event handler type names (default "Handler")
-out string
    Output source file name (default "evon_gen.go")
-show
    Show event handler types without generation
```

## FAQ

**How fast is evon?**

Evon maintains all subscribers on a dispatcher in a mere slice, so emitting an event is just iterating over the slice and calling the functions. Subscribing and unsubscribing are also fast O(1) operations, even if the removed item was not at the end of the slice.  However, the slice is always compact and never leave spaces for removed items, i.e. the iteration always involves existing members only.

**Does evon use reflection?**

No, evon does not use reflection at all. If it did, code generation might be unnecessary.

**Is the order of the subscribers preserved?**

No it's not. Users should never rely on the execution order of subscribers, even while using default "synchronous" dispatchers.

**Can a handler subscribe to a dispatcher for multiple times?**

Yes, but there's no deduplication, so the dispatcher sees it no different from multiple individual handlers, resulting in multiple invocations upon single emission.

**What if I strongly need passing results back to the emitter from handlers?**

A common pattern is using pointers / callbacks / other dispatchers ( like "response topic"s in message systems ) as a parameter.

**Can I generate emitters just for part of the methods of an interface as they are not all needed?**

You could define a new interface that just covers the needed part of the original interface and generate dispatchers upon it. This approach can also make unimplementable interfaces implementable to allow chaining.

**Can code generation work even when my code does not compile?**

It depends on how much your code is malformed and how much information the parser can extract form the good part of the code. Though difficult to generalize, it's certain that incompilable code will not always make code generation impossible. Wish you good luck.

**While developing, how can I temporarily disable an annotation while you can't comment out a comment?**

Any trick that breaks the format of the annotation will work, like `@/evon()`, `@ev on()`, etc. Annotations tolerate redundant whitespaces and commas within the parentheses, but what else need to be verbatim.