/*
Copyright (C) 2020 Alex Neo

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

package main

import(
  "fmt"
  corev1 "k8s.io/api/core/v1"
  "k8s.io/client-go/informers"
	"k8s.io/client-go/tools/cache"
  "github.com/alexnjh/epsilon/general_purpose_scheduler/scheduler"
)

// Add monitors to monitor global state and update local state if needed
// There are two types of monitors, one monitor pods and another one monitor nodes
func addAllEventHandlers(
  sched *scheduler.Scheduler,
  informerFactory informers.SharedInformerFactory,
  nodeInformer cache.SharedIndexInformer,
  podInformer cache.SharedIndexInformer,
  ){

    nodeInformer.AddEventHandler(cache.ResourceEventHandlerFuncs{

      // A new node is added
      AddFunc: func(obj interface{}) {

        node := obj.(*corev1.Node)
        err := sched.SchedulerCache.AddNode(node)

        if err != nil{
          fmt.Println("Fail to add node to cache", err)
        }

      },
      // One of the nodes got updated information
      UpdateFunc: func(oldObj, newObj interface{}) {
        oldNode := oldObj.(*corev1.Node)
        newNode := newObj.(*corev1.Node)

        err := sched.SchedulerCache.UpdateNode(oldNode,newNode)

        if err != nil{
          fmt.Println("Fail to update node to cache", err)
        }
      },
      // A node is deleted
      DeleteFunc: func(obj interface{}) {
        node := obj.(*corev1.Node)
        err := sched.SchedulerCache.RemoveNode(node)

        if err != nil{
          fmt.Println("Fail to remove node from cache", err)
        }

      },
    })

    podInformer.AddEventHandler(cache.ResourceEventHandlerFuncs{
      // A new pod is added
      AddFunc: func(obj interface{}) {


        pod := obj.(*corev1.Pod)

        err := sched.SchedulerCache.AddPod(pod)

        if err != nil{
          fmt.Println("Fail to add pod to cache", err)
        }
      },
      // One of the pods got updated information
      UpdateFunc: func(oldObj, newObj interface{}) {

        oldPod := oldObj.(*corev1.Pod)
        newPod := newObj.(*corev1.Pod)

        err := sched.SchedulerCache.UpdatePod(oldPod,newPod)

        if err != nil{
          fmt.Println("Fail to update pod to cache", err)
        }

      },
      // A pod is deleted
      DeleteFunc: func(obj interface{}) {
        pod := obj.(*corev1.Pod)
        err := sched.SchedulerCache.RemovePod(pod)

        if err != nil{
          fmt.Println("Fail to remove Pod from cache", err)
        }

      },
    })


}
