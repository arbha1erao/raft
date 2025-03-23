package utils

func RemoveSliceElementInPlace(slice *[]int, value int) {
	newLen := 0
	for _, v := range *slice {
		if v != value {
			(*slice)[newLen] = v
			newLen++
		}
	}
	*slice = (*slice)[:newLen]
}
