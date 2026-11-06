package fingerprint

import "math"

// DBSCAN labels of the unknown-algorithm clustering.
const (
	labelUnclassified = -1
	labelNoise        = -2
)

// standardize z-scores every feature dimension of the points. A dimension with
// zero variance becomes 0: it carries no information for the distance.
func standardize(points [][]float64) [][]float64 {
	if len(points) == 0 {
		return nil
	}
	dims := len(points[0])
	means := make([]float64, dims)
	for _, point := range points {
		for dim := 0; dim < dims; dim++ {
			means[dim] += point[dim]
		}
	}
	for dim := range means {
		means[dim] /= float64(len(points))
	}
	stddevs := make([]float64, dims)
	for _, point := range points {
		for dim := 0; dim < dims; dim++ {
			delta := point[dim] - means[dim]
			stddevs[dim] += delta * delta
		}
	}
	for dim := range stddevs {
		stddevs[dim] = math.Sqrt(stddevs[dim] / float64(len(points)))
	}
	out := make([][]float64, len(points))
	for index, point := range points {
		row := make([]float64, dims)
		for dim := 0; dim < dims; dim++ {
			if stddevs[dim] > 0 {
				row[dim] = (point[dim] - means[dim]) / stddevs[dim]
			}
		}
		out[index] = row
	}
	return out
}

// euclidean returns the Euclidean distance of two same-length points.
func euclidean(a, b []float64) float64 {
	var sum float64
	for index := range a {
		delta := a[index] - b[index]
		sum += delta * delta
	}
	return math.Sqrt(sum)
}

// regionQuery returns the point indexes inside the eps neighbourhood of i.
func regionQuery(points [][]float64, i int, eps float64) []int {
	out := make([]int, 0, 8)
	for j := range points {
		if euclidean(points[i], points[j]) <= eps {
			out = append(out, j)
		}
	}
	return out
}

// dbscanLabels runs the classic DBSCAN algorithm over the standardized points.
// It returns one label per point: a cluster index >= 0, labelNoise for a point
// outside every cluster. The iteration order is the point order, so the result is
// deterministic.
func dbscanLabels(points [][]float64, eps float64, minPoints int) []int {
	labels := make([]int, len(points))
	for index := range labels {
		labels[index] = labelUnclassified
	}
	if eps <= 0 || minPoints < 2 {
		return labels
	}
	visited := make([]bool, len(points))
	cluster := 0
	for i := range points {
		if visited[i] {
			continue
		}
		visited[i] = true
		neighbours := regionQuery(points, i, eps)
		if len(neighbours) < minPoints {
			labels[i] = labelNoise
			continue
		}
		labels[i] = cluster
		seeds := append([]int(nil), neighbours...)
		for j := 0; j < len(seeds); j++ {
			point := seeds[j]
			if !visited[point] {
				visited[point] = true
				if expanded := regionQuery(points, point, eps); len(expanded) >= minPoints {
					seeds = append(seeds, expanded...)
				}
			}
			if labels[point] == labelUnclassified || labels[point] == labelNoise {
				labels[point] = cluster
			}
		}
		cluster++
	}
	return labels
}

// clusterCohesion returns 1 for points packed at the core of the eps ball and 0
// when their mean pairwise distance reaches eps.
func clusterCohesion(points [][]float64, members []int, eps float64) float64 {
	if len(members) < 2 || eps <= 0 {
		return 0
	}
	var total float64
	pairs := 0
	for i := 0; i < len(members); i++ {
		for j := i + 1; j < len(members); j++ {
			total += euclidean(points[members[i]], points[members[j]])
			pairs++
		}
	}
	if pairs == 0 {
		return 0
	}
	return clamp01(1 - total/float64(pairs)/eps)
}
