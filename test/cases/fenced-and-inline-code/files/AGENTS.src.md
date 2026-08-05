# Fenced and inline code test

Three-tick fence with a fake import inside (must not expand):

```
See @child.md here, should stay literal.
```

Four-tick fence containing a three-tick fence (must stay verbatim, all of it):

````
Outer fence start.
```
Inner three-tick fence line with @child.md, still not expanded.
```
Outer fence end.
````

A line with both inline code and a bare import: `@child.md` and @child.md

End of doc.
