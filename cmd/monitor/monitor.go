package main

import (
	"flag"
	"fmt"
	"math/rand"
	"net/http"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/aws/aws-sdk-go/aws/session"
	"github.com/aws/aws-sdk-go/service/ec2"
	"github.com/aws/aws-sdk-go/aws/awserr"
//	"github.com/aws/aws-sdk-go/aws"
)

type TransitionState int

const (
	transitioning TransitionState = iota
    stuck
    unstuck
)

const(
	stuckAfterTolerance = 5 // TODO: make this a command line param.
)

func getVolumes() ([]*ec2.Volume, error) {
	sess, err := session.NewSession()
	svc := ec2.New(sess)
/*	input := &ec2.DescribeVolumesInput{
		Filters: []*ec2.Filter{
			{
				Name: aws.String("attachment.status"),
				Values: []*string{
					aws.String("attached"),
					aws.String("detached"),
				},
			},
		},
	}
*/

	input := &ec2.DescribeVolumesInput{}

	results, err := svc.DescribeVolumes(input)
	if err != nil {
		if aerr, ok := err.(awserr.Error); ok {
			switch aerr.Code() {
			default:
				fmt.Println(aerr.Error())
			}
		} else {
			// Print the error, cast err to awserr.Error to get the Code and
			// Message from an error.
			fmt.Println(err.Error())
		}
		return nil, err
	}

	return results.Volumes, nil
}

type VolumeStore struct {
state TransitionState
volume *ec2.Volume
stuck_after time.Time
}

type AttachmentState string

const (
	attaching AttachmentState = "attaching"
	detaching AttachmentState = "detaching"
	attached AttachmentState = "attached"
	detached AttachmentState = "detached"
)

func translateAttachmentState(volume *ec2.Volume) AttachmentState {
	attachment_states := map[string]int {}

	for _, attachment := range volume.Attachments {
		attachment_states[*attachment.State] += 1
	}

	if _, present := attachment_states["attaching"]; present {
		return attaching
	}

	if _, present := attachment_states["detaching"]; present {
		return detaching
	}

	if _, present := attachment_states["attached"]; present {
		return attached
	}

	// default to detached. This will handle the nil case as well.
	return detached
}

func getVolumesInTransitionAttachmentState(volumes []*ec2.Volume) []*ec2.Volume {
	retval := []*ec2.Volume {}

	for _, volume := range volumes {
		attachment_state := translateAttachmentState(volume)

		if attachment_state == detaching || attachment_state == attaching {
			retval = append(retval, volume)
		}
	}

	return retval
}

func main() {
	volume_states := map[string]VolumeStore {}

	volumes, _ := getVolumes()
	volumes = getVolumesInTransitionAttachmentState(volumes)

    for ix, volume := range volumes {
		vol_uri := generateVolumeUri(volume)

		v := VolumeStore {
			state: transitioning,
			volume: volume,
			stuck_after: time.Now().Add(time.Duration(stuckAfterTolerance) * time.Second),
		}

		volume_states[vol_uri] = v

		fmt.Printf("%d: %s: %s: %s\n", ix, *volume.VolumeId, translateAttachmentState(volume), vol_uri)
		fmt.Println(v.stuck_after.Format(time.ANSIC))
    }



	//fmt.Println(results)

//	sess  := session.Must(session.NewSession())

//	if sess == nil {
//		panic("unable to load SDK config")
//	}



	addr := flag.String("listen-address", ":8080", "The address to listen on for HTTP requests.")
	flag.Parse()

	http.HandleFunc("/healthz", handleHealthz)
	http.Handle("/metrics", prometheus.Handler())

	appCreateLatency := prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "app_create_latency_seconds",
			Help:    "The latency of various app creation steps.",
			Buckets: []float64{1, 10, 60, 3 * 60, 5 * 60},
		},
		[]string{"step"},
	)
	prometheus.MustRegister(appCreateLatency)

	go http.ListenAndServe(*addr, nil)

	go runAppCreateSim(appCreateLatency, 1*time.Second)

	select {}
}


func generateVolumeUri(vol *ec2.Volume) string {
	//return fmt.Sprintf("aws://%s/%s", "us-east-1c", "vol-11fde6b3")
	return fmt.Sprintf("aws://%s/%s", *vol.AvailabilityZone, *vol.VolumeId)
}


func handleHealthz(w http.ResponseWriter, r *http.Request) {
	fmt.Fprintf(w, "ok")
}

func runAppCreateSim(metric *prometheus.HistogramVec, interval time.Duration) {
	steps := map[string]struct {
		min time.Duration
		max time.Duration
	}{
		"new-app": {min: 1 * time.Second, max: 5 * time.Second},
		"build":   {min: 1 * time.Minute, max: 5 * time.Minute},
		"deploy":  {min: 1 * time.Minute, max: 5 * time.Minute},
		"expose":  {min: 10 * time.Second, max: 1 * time.Minute},
	}
	rng := rand.New(rand.NewSource(time.Now().UnixNano()))
	for {
		for step, r := range steps {
			latency := rng.Int63n(int64(r.max)-int64(r.min)) + int64(r.min)
			metric.With(prometheus.Labels{"step": step}).Observe(float64(latency / int64(time.Second)))
		}
		time.Sleep(interval)
	}
}
