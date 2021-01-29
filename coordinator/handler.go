/*
Copyright 2017 The Kubernetes Authors.
Modification copyright (C) 2020 Alex Neo

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.

*/

package coordinator

import (
  "fmt"
  "encoding/json"
  "time"
  "math/rand"

  "k8s.io/client-go/kubernetes"
  "k8s.io/client-go/tools/cache"
  "k8s.io/apimachinery/pkg/api/errors"
  "github.com/prometheus/client_golang/prometheus"

  log "github.com/sirupsen/logrus"
  utilruntime "k8s.io/apimachinery/pkg/util/runtime"
  corelisters "k8s.io/client-go/listers/core/v1"
  corev1 "k8s.io/api/core/v1"
)

// Handler interface contains the methods that are required
type Handler interface {
	Init() error
	ObjectSync(key string) error
	ObjectDeleted(key string)
}

// PodHandler is a implementation of Handler
type PodHandler struct{
  defaultQueue string
  hostname  string
  clientset kubernetes.Interface
  lister  corelisters.PodLister
  comm communication.Communication
  metricCounter prometheus.Counter
}

// Init handles any handler initialization
func (t *PodHandler) Init() error {
	log.Info("PodHandler.Init")
	return nil
}

// ObjectSync is called when an object is created
func (t *PodHandler) ObjectSync(key string) error {

  timeStamp := time.Now();

  // Convert the namespace/name string into a distinct namespace and name
	namespace, name, err := cache.SplitMetaNamespaceKey(key)
	if err != nil {
		utilruntime.HandleError(fmt.Errorf("invalid resource key: %s", key))
		return nil
	}

	// Get the pod resource with this namespace/name
	obj, err := t.lister.Pods(namespace).Get(name)
	if err != nil {
		if errors.IsNotFound(err) {
			utilruntime.HandleError(fmt.Errorf("Pod '%s' in work queue no longer exists", key))
			return nil
		}

		return err
	}

  var queueName string

  // Get queue information to calculate waiting time
  if len(obj.Labels["epsilon.queue"]) != 0 {
    queueName = obj.Labels["epsilon.queue"]
  }else{
    queueName = t.defaultQueue
  }
  //
  // qs, err := t.rmqc.GetQueue("/",queueName)

  // // If information not obtainable the waiting time will not be calculated and set to unknown
  // // Coordinator will still attempt to send the pod for scheduling
  // if err != nil{
  //   log.Errorf("Cannot get queue:  %s",err.Error())
  //
  //   // Update pod estimated waiting time
  //   if obj.Annotations != nil {
  //     obj.Annotations["epsilon.scheduling.waiting_time"] = "Unknown"
  //   }else{
  //     obj.Annotations=map[string]string{
  //       "epsilon.scheduling.waiting_time": "Unknown",
  //     }
  //   }
  // }else{
  //
  //   estimate := calWaitingTime(qs.Consumers,qs.Messages)
  //
  //   // Update pod estimated waiting time
  //   if obj.Annotations != nil {
  //     obj.Annotations["epsilon.scheduling.waiting_time"] = (time.Duration(estimate)*time.Millisecond).String()
  //   }else{
  //     obj.Annotations=map[string]string{
  //       "epsilon.scheduling.waiting_time": (time.Duration(estimate)*time.Millisecond).String(),
  //     }
  //   }
  // }

  // // Update pod information and if there is an error the scheduling request will continue
  // // but waiting time will not be written to the api server
  // _ , err = t.clientset.CoreV1().Pods(obj.Namespace).Update(context.TODO(), obj, metav1.UpdateOptions{});

  //
  // if err != nil {
  //   log.Errorf(err.Error())
  // }

  // Keep trying if unable to send schedule request to the queued.
  // This happens when connection to the rabbitmq server might be down.
  // The pod coordinator will keep trying as if send to backoff the next pod requested
  // will also fail due to connection failure.
  for {
    if t.sendScheduleRequest(key,timeStamp,queueName) == false {

      for{
        err := t.comm.Connect()
        if err == nil{
          break
        }
        // Sleep for a random time before trying again
        time.Sleep(time.Duration(rand.Intn(10))*time.Second)
      }
    }else{
      break
    }
  }

  for {
    if t.sendExperimentPayload(obj, timeStamp, time.Now(), "epsilon.experiment", t.hostname) == false {

      for{
        err = t.comm.Connect()
        if err == nil{
          break
        }
        // Sleep for a random time before trying again
        time.Sleep(time.Duration(rand.Intn(10))*time.Second)
      }
    }else{
      break
    }
  }

  // // Increase pod count by 1
  // t.metricCounter.Inc()

  return nil

}

// Send schedule request to the schedulers
func (t *PodHandler) sendScheduleRequest(key string, timestamp time.Time, queueName string) bool{

  timeElapsed := time.Since(timestamp);

  respBytes, err := json.Marshal(ScheduleRequest{Key:key,LastBackOffTime:2,ProcessedTime:timeElapsed,Message: ""})
  if err != nil {
    log.Fatalf("%s", err)
  }

  err = t.comm.Send(respBytes,queueName)

  if err != nil{
    return false
  }

  return true
}

func (t *PodHandler) sendExperimentPayload(pod *corev1.Pod, in time.Time, out time.Time, queueName string, hostname string) bool{

  respBytes, err := json.Marshal(ExperimentPayload{Type:"Coordinator",InTime:in,OutTime:out,Pod:pod,Hostname: hostname})
  if err != nil {
    log.Fatalf("%s", err)
  }

  err = t.comm.Send(respBytes,queueName)

  if err != nil{
    return false
  }

  return true
}

// ObjectDeleted is called when an object is deleted
func (t *PodHandler) ObjectDeleted(key string) {

	log.Info("[TestHandler] Object Deleted")

}

// Calculates the waiting time for the pod to be scheduled
func calWaitingTime(noOfConsumers, noOfMessages int) int{

  if noOfMessages == 0 {
    return AverageSchedulingTime
  }

  // Not completed testing only
  return int((float64(noOfMessages)/float64(noOfConsumers))*AverageSchedulingTime)
}
