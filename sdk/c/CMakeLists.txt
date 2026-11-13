# gs_sdk: a static and a shared library, and the sample. Any C99 compiler; Windows links ws2_32.
cmake_minimum_required(VERSION 3.16)
project(gs_sdk C)

add_library(gs_sdk STATIC src/gs_sdk.c)
target_include_directories(gs_sdk PUBLIC include)
set_target_properties(gs_sdk PROPERTIES C_STANDARD 99 POSITION_INDEPENDENT_CODE ON)

add_library(gs_sdk_shared SHARED src/gs_sdk.c)
target_include_directories(gs_sdk_shared PUBLIC include)
target_compile_definitions(gs_sdk_shared PUBLIC GS_SDK_SHARED)
set_target_properties(gs_sdk_shared PROPERTIES C_STANDARD 99 OUTPUT_NAME gs_sdk C_VISIBILITY_PRESET hidden)

if(WIN32)
  target_link_libraries(gs_sdk PUBLIC ws2_32)
  target_link_libraries(gs_sdk_shared PUBLIC ws2_32)
else()
  find_package(Threads REQUIRED)
  target_link_libraries(gs_sdk PUBLIC Threads::Threads)
  target_link_libraries(gs_sdk_shared PUBLIC Threads::Threads)
endif()

add_executable(gs_sample sample/sample.c)
target_link_libraries(gs_sample gs_sdk)
