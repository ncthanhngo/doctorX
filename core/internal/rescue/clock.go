package rescue

import "time"

// nowFunc tách ra để test cố định được thời gian.
var nowFunc = time.Now
