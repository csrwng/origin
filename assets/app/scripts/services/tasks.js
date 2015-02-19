'use strict';

// TaskList is a set of tasks that need to be executed or 
// are in progress.
// Current set of tasks is displayed in the overview page.
// Each task has the following members
//  - titles - { started, failure, success } - Titles to use depending on 
//             state of the task
//  - helpLinks - array of { title :string, url: string }
//  - action - a function that kicks off the task and returns a promise
//             the promise resolve needs to be resolved with a structure
//             containing the following fields:
//                 - alerts - array of alerts to display
//                 - hasErrors - true if errors occurred processing the action  
//  - status - whether task is: new, started, completed
//  - alerts - alerts generated by the task
//  - completedTime - time that the task was completed (for garbage collection)

angular.module('openshiftConsole')
.factory('TaskList', function($interval) {

  // Maximum amount of time that a task will hang around after completion
  var TASK_TIMEOUT = 30*1000;
  
  // How often the task list will be checked
  var TASK_REFRESH_INTERVAL = 100;

  function TaskList() {
    this.tasks = []
  }
  
  var taskList = new TaskList()
  
  TaskList.prototype.add = function(titles, helpLinks, action) {
    this.tasks.push({
      titles: titles,
      helpLinks: helpLinks,
      action: action,
      status: "new"
    });
  }
  
  TaskList.prototype.taskList = function() {
    return this.tasks
  }
  
  TaskList.prototype._handleTasks = function() {
    var newList = [];
    var tasks = this.tasks;
    tasks.forEach(function(task) {
      if (task.status == "new") {
        task.status = "started";
        task.action().then(function(result) {
          task.hasErrors = result.hasErrors;
          task.alerts = result.alerts;
          task.status = "completed";
          task.completedTime = Date.now();
        });
      }
      else if (task.status == "completed" && !task.hasError) {
        // Clear out successfully completed tasks after a timeout
        if ((Date.now() - task.completedTime) > TASK_TIMEOUT) {
          task.toDelete = true;
        }
      }
    });
    var index = 0;
    while (index < tasks.length) {
      if (tasks[index].toDelete) {
        tasks.splice(index,1);
      }
      else {
        index++;
      }
    }
  }
  
  var taskList = new TaskList();
  $interval(function() { taskList._handleTasks(); }, TASK_REFRESH_INTERVAL);
  return taskList;
});