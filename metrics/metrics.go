// Package metrics provides metrics used throughout the program, and also exports the maintenance status of every site and machine.
package metrics

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

var (
	// Error is a prometheus metric for exposing any errors that the exporter encounters.
	//
	// TODO: change to gmx_error_total in keeping with prometheus best practices
	// as expressed by their linter.
	Error = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "gmx_error_count",
			Help: "Count of errors.",
		},
		[]string{
			"type",
			"function",
		},
	)
	// Machine is a prometheus metric for exposing machine maintenance status.
	Machine = promauto.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "gmx_machine_maintenance",
			Help: "Whether a machine is in maitenance mode or not.",
		},
		[]string{
			"machine",
			"node",
		},
	)
	// Site is a prometheus metric for exposing site maintenance status.
	Site = promauto.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "gmx_site_maintenance",
			Help: "Whether a site is in maintenance mode or not.",
		},
		[]string{
			"site",
		},
	)
)
